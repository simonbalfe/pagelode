package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"github.com/simonbalfe/pagelode/internal/api"
	"github.com/simonbalfe/pagelode/internal/store"
	"github.com/simonbalfe/pagelode/pkg/browser"
	"github.com/simonbalfe/pagelode/pkg/discovery"
	"github.com/simonbalfe/pagelode/pkg/emails"
	"github.com/simonbalfe/pagelode/pkg/fetch"
	"github.com/simonbalfe/pagelode/pkg/sitemap"
)

const version = "0.1.0"

type application struct {
	emailFinder    *emails.Service
	mapper         *sitemap.Service
	extractor      *fetch.Service
	discoverer     *discovery.Service
	extractLimiter *fetch.Limiter
	routes         *fetch.Routes
	store          *store.Store
}

func Run(args []string, stdout io.Writer, stderr io.Writer) error {
	if len(args) == 0 {
		writeRootHelp(stdout)
		return nil
	}
	if handled, err := handleHelp(args, stdout); handled {
		return err
	}
	if args[0] == "version" || args[0] == "--version" || args[0] == "-v" {
		fmt.Fprintf(stdout, "PageLode %s\n", version)
		return nil
	}

	log.SetOutput(io.Discard)
	configuration, err := LoadConfig()
	if err != nil {
		return fmt.Errorf("load configuration: %w", err)
	}
	if args[0] == "emails" {
		return emailsCommand(configuration, args[1:], stdout, stderr)
	}
	if args[0] == "extract" && len(args) > 1 && args[1] == "batch" {
		return batchCommand(configuration, args[2:], stdout, stderr)
	}
	app, err := newApplication(context.Background(), configuration)
	if err != nil {
		return err
	}
	defer app.Close()

	if args[0] == "serve" {
		if len(args) != 1 {
			return errors.New("serve does not accept arguments")
		}
		return app.serve(configuration, stdout)
	}
	if args[0] == "profile" {
		return profileCommand(configuration, args[1:], stdout, stderr)
	}
	if args[0] == "map" {
		if len(args) > 1 && args[1] == "endpoints" {
			return app.endpoints(configuration, args[2:], stdout, stderr)
		}
		return app.siteMap(configuration, args[1:], stdout, stderr)
	}
	if args[0] == "extract" {
		args = args[1:]
	}
	return app.extract(configuration, args, stdout, stderr)
}

func newApplication(ctx context.Context, configuration Config) (*application, error) {
	if configuration.DatabaseURL == "" {
		return wire(configuration, nil)
	}
	persistence, err := store.Open(ctx, configuration.DatabaseURL)
	if err != nil {
		return nil, err
	}
	app, err := wire(configuration, persistence)
	if err != nil {
		persistence.Close()
		return nil, err
	}
	return app, nil
}

func wire(configuration Config, persistence *store.Store) (*application, error) {
	routes := fetch.NewRoutes(configuration.RouteTTL, configuration.ProtectedDomains)
	clearances := fetch.NewClearances(configuration.RouteTTL)
	var generations browser.GenerationStore
	if persistence != nil {
		if err := routes.Restore(persistence); err != nil {
			return nil, err
		}
		if err := clearances.Restore(persistence); err != nil {
			return nil, err
		}
		generations = persistence
	}
	identities, err := browser.NewIdentities(configuration.ProxyURL, configuration.BrowserIdentities, generations)
	if err != nil {
		return nil, err
	}
	browserClient, err := browser.New(configuration.BrowserURLs, configuration.BrowserToken, configuration.BrowserSettle, configuration.BrowserNodeSlots, identities)
	if err != nil {
		return nil, fmt.Errorf("configure browser service: %w", err)
	}
	extractLimiter := fetch.NewLimiter(configuration.MaxConcurrency, configuration.MaxWaiting)
	httpClient := fetch.NewFollowingHTTPClient(identities.ProxyFor)
	capSolverFetcher, captchaTasks, err := capSolver(configuration)
	if err != nil {
		return nil, err
	}
	renderer := browser.NewSolver(browserClient, captchaTasks, httpClient)
	emailLoader := emails.NewLoader(httpClient, renderer, capSolverFetcher, routes)
	return &application{
		mapper:         sitemap.New(httpClient, extractLimiter).WithBrowser(browserClient),
		emailFinder:    emails.New(emailLoader, extractLimiter, configuration.MaxConcurrency),
		discoverer:     discovery.New(renderer).WithProxy(configuration.ProxyURL != ""),
		extractor:      fetch.NewService(httpClient, renderer, capSolverFetcher, routes, clearances),
		extractLimiter: extractLimiter,
		routes:         routes,
		store:          persistence,
	}, nil
}

func capSolver(configuration Config) (fetch.Fetcher, browser.Tasks, error) {
	api := browser.NewCapSolverAPI(configuration.CapSolverAPIKey, configuration.CapSolverURL)
	if api == nil {
		return nil, nil, nil
	}
	proxyURL := configuration.CapSolverProxyURL
	if proxyURL != "" {
		resolveContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		resolved, err := browser.ResolveProxy(resolveContext, proxyURL)
		cancel()
		if err != nil {
			return nil, nil, fmt.Errorf("configure CapSolver: %w", err)
		}
		proxyURL = resolved
	}
	solverHTTP := fetch.NewHTTPClient(proxyURL)
	client, err := browser.NewCapSolver(api, proxyURL, solverHTTP, solverHTTP)
	if err != nil {
		return nil, nil, fmt.Errorf("configure CapSolver: %w", err)
	}
	if client == nil {
		return nil, api, nil
	}
	return client, api, nil
}

func defineExtractFlags(flags *flag.FlagSet) *bool {
	jsonOutput := flags.Bool("json", false, "print the complete JSON result")
	flags.BoolVar(jsonOutput, "j", false, "print the complete JSON result")
	return jsonOutput
}

func (a *application) extract(configuration Config, args []string, stdout io.Writer, stderr io.Writer) error {
	flags := newFlagSet("", stderr)
	jsonOutput := defineExtractFlags(flags)
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			writeRootHelp(stdout)
			return nil
		}
		return err
	}
	if flags.NArg() != 1 {
		return errors.New("provide one URL or domain")
	}

	ctx, cancel := context.WithTimeout(context.Background(), configuration.RequestTimeout)
	defer cancel()
	result, err := a.extractor.Extract(ctx, flags.Arg(0))
	if err != nil {
		return err
	}
	if *jsonOutput {
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(result); err != nil {
			return fmt.Errorf("write result: %w", err)
		}
	} else if result.Content != "" {
		if _, err := fmt.Fprintln(stdout, result.Content); err != nil {
			return fmt.Errorf("write content: %w", err)
		}
	}
	if result.Outcome != "ok" {
		return fmt.Errorf("extraction %s%s", result.Outcome, attemptDetail(result.Attempts))
	}
	return nil
}

func (a *application) serve(configuration Config, output io.Writer) error {
	logger := slog.New(slog.NewJSONHandler(output, nil))
	apiServer := api.New(a.extractor, a.discoverer, a.extractLimiter, a.routes, configuration.RequestTimeout, logger).WithEmails(a.emailFinder).WithMap(a.mapper)
	server := &http.Server{
		Addr:              configuration.Address,
		Handler:           apiServer.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       20 * time.Second,
		WriteTimeout:      configuration.RequestTimeout + 5*time.Second,
		IdleTimeout:       60 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			logger.Error("shutdown HTTP server", "error", err)
		}
	}()

	logger.Info("pagelode listening", "address", configuration.Address, "protected_domains", configuration.ProtectedDomains, "persisted_state", configuration.DatabaseURL != "")
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serve: %w", err)
	}
	return nil
}

func (a *application) Close() {
	if a.store != nil {
		a.store.Close()
	}
}

func attemptDetail(attempts []fetch.Attempt) string {
	if len(attempts) == 0 || attempts[len(attempts)-1].Detail == "" {
		return ""
	}
	return ": " + attempts[len(attempts)-1].Detail
}
