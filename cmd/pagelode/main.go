package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/simonbalfe/pagelode/internal/api"
	"github.com/simonbalfe/pagelode/internal/browser"
	"github.com/simonbalfe/pagelode/internal/capsolver"
	"github.com/simonbalfe/pagelode/internal/captcha"
	"github.com/simonbalfe/pagelode/internal/config"
	"github.com/simonbalfe/pagelode/internal/discovery"
	"github.com/simonbalfe/pagelode/internal/emails"
	"github.com/simonbalfe/pagelode/internal/httpfetch"
	"github.com/simonbalfe/pagelode/internal/limit"
	"github.com/simonbalfe/pagelode/internal/memory"
	"github.com/simonbalfe/pagelode/internal/orchestrator"
	"github.com/simonbalfe/pagelode/internal/sitemap"
	"github.com/simonbalfe/pagelode/internal/state"
)

const version = "0.1.0"

type application struct {
	emailFinder    *emails.Service
	mapper         *sitemap.Service
	extractor      *orchestrator.Service
	discoverer     *discovery.Service
	extractLimiter *limit.Limiter
	routes         *memory.Routes
	state          *state.Store
}

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "pagelode:", err)
		os.Exit(1)
	}
}

func run(args []string, stdout io.Writer, stderr io.Writer) error {
	if len(args) == 0 || args[0] == "help" {
		writeUsage(stdout)
		return nil
	}
	if args[0] == "version" || args[0] == "--version" || args[0] == "-v" {
		fmt.Fprintf(stdout, "PageLode %s\n", version)
		return nil
	}

	configuration, err := config.Load()
	if err != nil {
		return fmt.Errorf("load configuration: %w", err)
	}
	if args[0] == "emails-bulk" {
		return emailsBulk(configuration, args[1:], stdout, stderr)
	}
	app, err := newApplication(configuration)
	if err != nil {
		return err
	}
	defer func() {
		if err := app.Close(); err != nil {
			fmt.Fprintln(stderr, "pagelode: close:", err)
		}
	}()

	if args[0] == "serve" {
		if len(args) != 1 {
			return errors.New("serve does not accept arguments")
		}
		return app.serve(configuration, stdout)
	}
	if args[0] == "profile" {
		return profileCommand(configuration, args[1:], stdout, stderr)
	}
	if args[0] == "emails" {
		return app.emails(configuration, args[1:], stdout, stderr)
	}
	if args[0] == "map" {
		return app.siteMap(configuration, args[1:], stdout, stderr)
	}
	if args[0] == "discover" {
		return app.discover(configuration, args[1:], stdout, stderr)
	}
	if args[0] == "extract" {
		args = args[1:]
	}
	return app.extract(configuration, args, stdout, stderr)
}

func newApplication(configuration config.Config) (*application, error) {
	store, err := state.Open(configuration.StatePath)
	if err != nil {
		return nil, fmt.Errorf("open state %s: %w", configuration.StatePath, err)
	}
	app, err := wire(configuration, store)
	if err != nil {
		return nil, errors.Join(err, store.Close())
	}
	return app, nil
}

func wire(configuration config.Config, store *state.Store) (*application, error) {
	routes := memory.NewRoutes(configuration.RouteTTL, configuration.ProtectedDomains)
	if err := routes.Restore(store); err != nil {
		return nil, err
	}
	clearances := memory.NewClearances(configuration.RouteTTL)
	if err := clearances.Restore(store); err != nil {
		return nil, err
	}
	identities, err := browser.NewIdentities(configuration.ProxyURL, store)
	if err != nil {
		return nil, err
	}
	browserClient, err := browser.New(configuration.BrowserURL, configuration.BrowserToken, configuration.BrowserSettle, identities)
	if err != nil {
		return nil, fmt.Errorf("configure browser service: %w", err)
	}
	extractLimiter := limit.New(configuration.MaxConcurrency, configuration.MaxWaiting)
	httpClient := httpfetch.Following(identities.CurrentProxy)
	capSolverFetcher, captchaTasks, err := capSolver(configuration)
	if err != nil {
		return nil, err
	}
	renderer := captcha.New(browserClient, captchaTasks, httpClient)
	emailLoader := emails.NewLoader(httpClient, renderer, capSolverFetcher, routes)
	return &application{
		mapper:         sitemap.New(httpClient, extractLimiter),
		emailFinder:    emails.New(emailLoader, extractLimiter, configuration.MaxConcurrency),
		discoverer:     discovery.New(renderer).WithProxy(configuration.ProxyURL != ""),
		extractor:      orchestrator.New(httpClient, renderer, capSolverFetcher, routes, clearances),
		extractLimiter: extractLimiter,
		routes:         routes,
		state:          store,
	}, nil
}

func capSolver(configuration config.Config) (orchestrator.Fetcher, captcha.Tasks, error) {
	api := capsolver.NewAPI(configuration.CapSolverAPIKey, configuration.CapSolverURL)
	if api == nil {
		return nil, nil, nil
	}
	proxyURL := configuration.CapSolverProxyURL
	if proxyURL != "" {
		resolveContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		resolved, err := capsolver.ResolveProxy(resolveContext, proxyURL)
		cancel()
		if err != nil {
			return nil, nil, fmt.Errorf("configure CapSolver: %w", err)
		}
		proxyURL = resolved
	}
	solverHTTP := httpfetch.New(proxyURL)
	client, err := capsolver.New(api, proxyURL, solverHTTP, solverHTTP)
	if err != nil {
		return nil, nil, fmt.Errorf("configure CapSolver: %w", err)
	}
	if client == nil {
		return nil, api, nil
	}
	return client, api, nil
}

func (a *application) extract(configuration config.Config, args []string, stdout io.Writer, stderr io.Writer) error {
	flags := flag.NewFlagSet("pagelode", flag.ContinueOnError)
	flags.SetOutput(stderr)
	jsonOutput := flags.Bool("json", false, "print the complete JSON result")
	flags.BoolVar(jsonOutput, "j", false, "print the complete JSON result")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			writeUsage(stdout)
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

func (a *application) serve(configuration config.Config, output io.Writer) error {
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

	logger.Info("pagelode listening", "address", configuration.Address, "protected_domains", configuration.ProtectedDomains, "state", configuration.StatePath)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serve: %w", err)
	}
	return nil
}

func (a *application) Close() error {
	return a.state.Close()
}

func attemptDetail(attempts []orchestrator.Attempt) string {
	if len(attempts) == 0 || attempts[len(attempts)-1].Detail == "" {
		return ""
	}
	return ": " + attempts[len(attempts)-1].Detail
}

func writeUsage(output io.Writer) {
	fmt.Fprintln(output, `PageLode extracts Markdown, maps sites, discovers page data endpoints, and finds emails.

Usage:
  pagelode <URL>
  pagelode --json <URL>
  pagelode extract <URL>
  pagelode discover [--profile <name>] [--stealth] [--verbose] [--wait-ms 1500] <URL>
  pagelode discover --har <capture.har>
  pagelode emails [--max-pages 20] [--profile <name>] [--verbose] <URL>
  pagelode map [--limit 500] [--include /blog/*] [--exclude /admin/*] [--subdomains] [--json] <URL>
  pagelode emails-bulk [--db data/emails.sqlite] [--workers 32] [--max-pages 8] [--render never] <file|->
  pagelode profile login <name> <URL>
  pagelode serve
  pagelode version

Examples:
  pagelode example.com
  pagelode https://example.com/article
  pagelode --json example.com`)
}
