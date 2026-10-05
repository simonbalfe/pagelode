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
	"github.com/simonbalfe/pagelode/internal/capsolver"
	"github.com/simonbalfe/pagelode/internal/chromefetch"
	"github.com/simonbalfe/pagelode/internal/config"
	"github.com/simonbalfe/pagelode/internal/discovery"
	"github.com/simonbalfe/pagelode/internal/emails"
	"github.com/simonbalfe/pagelode/internal/httpfetch"
	"github.com/simonbalfe/pagelode/internal/limit"
	"github.com/simonbalfe/pagelode/internal/memory"
	"github.com/simonbalfe/pagelode/internal/orchestrator"
	"github.com/simonbalfe/pagelode/internal/patchright"
)

const version = "0.1.0"

type application struct {
	emailFinder       *emails.Service
	extractor         *orchestrator.Service
	discoverer        *discovery.Service
	extractLimiter    *limit.Limiter
	browserLimiter    *limit.Limiter
	routes            *memory.Routes
	chromedp          *chromefetch.Engine
	capSolverChromedp *chromefetch.Engine
	patchright        *patchright.Client
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
	if args[0] == "discover" {
		return app.discover(configuration, args[1:], stdout, stderr)
	}
	if args[0] == "extract" {
		args = args[1:]
	}
	return app.extract(configuration, args, stdout, stderr)
}

func newApplication(configuration config.Config) (*application, error) {
	patchrightClient, err := patchright.New(configuration.PatchrightCommand, []string{configuration.PatchrightWorker}, configuration.ProxyURL)
	if err != nil {
		return nil, fmt.Errorf("configure Patchright: %w", err)
	}
	chromedp, err := chromefetch.New(configuration.ProxyURL)
	if err != nil {
		return nil, fmt.Errorf("configure Chromedp: %w", err)
	}
	routes := memory.NewRoutes(configuration.RouteTTL, configuration.ProtectedDomains)
	browserLimiter := limit.New(configuration.BrowserConcurrency, configuration.MaxWaiting)
	extractLimiter := limit.New(configuration.MaxConcurrency, configuration.MaxWaiting)
	httpClient := httpfetch.New(configuration.ProxyURL)
	capSolverProxyURL := configuration.CapSolverProxyURL
	if configuration.CapSolverAPIKey != "" && capSolverProxyURL != "" {
		resolveContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		capSolverProxyURL, err = capsolver.ResolveProxy(resolveContext, capSolverProxyURL)
		cancel()
		if err != nil {
			return nil, fmt.Errorf("configure CapSolver: %w", err)
		}
	}
	capSolverHTTPClient := httpfetch.New(capSolverProxyURL)
	capSolverChromedp, err := chromefetch.New(capSolverProxyURL)
	if err != nil {
		return nil, fmt.Errorf("configure CapSolver browser: %w", err)
	}
	capSolverClient, err := capsolver.New(configuration.CapSolverAPIKey, configuration.CapSolverURL, capSolverProxyURL, capSolverHTTPClient, capSolverChromedp)
	if err != nil {
		return nil, fmt.Errorf("configure CapSolver: %w", err)
	}
	var capSolverFetcher orchestrator.Fetcher
	if capSolverClient != nil {
		capSolverFetcher = capSolverClient
	}
	extractor := orchestrator.New(
		httpClient,
		chromedp,
		patchrightClient,
		capSolverFetcher,
		routes,
		browserLimiter,
		configuration.ChromedpEnabled,
	)
	emailLoader := emails.NewLoader(httpClient, chromedp, patchrightClient, capSolverFetcher, routes, browserLimiter, configuration.ChromedpEnabled)
	return &application{
		emailFinder:       emails.New(emailLoader, extractLimiter, configuration.MaxConcurrency, configuration.ProfilesDirectory),
		discoverer:        discovery.New(chromedp, patchrightClient, routes, browserLimiter, configuration.ChromedpEnabled).WithProfiles(configuration.ProfilesDirectory),
		extractor:         extractor,
		extractLimiter:    extractLimiter,
		browserLimiter:    browserLimiter,
		routes:            routes,
		chromedp:          chromedp,
		capSolverChromedp: capSolverChromedp,
		patchright:        patchrightClient,
	}, nil
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
	apiServer := api.New(a.extractor, a.discoverer, a.extractLimiter, a.browserLimiter, a.routes, configuration.RequestTimeout, logger).WithEmails(a.emailFinder)
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

	logger.Info("pagelode listening", "address", configuration.Address, "chromedp", configuration.ChromedpEnabled, "protected_domains", configuration.ProtectedDomains)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serve: %w", err)
	}
	return nil
}

func (a *application) Close() error {
	return errors.Join(a.chromedp.Close(), a.capSolverChromedp.Close(), a.patchright.Close())
}

func attemptDetail(attempts []orchestrator.Attempt) string {
	if len(attempts) == 0 || attempts[len(attempts)-1].Detail == "" {
		return ""
	}
	return ": " + attempts[len(attempts)-1].Detail
}

func writeUsage(output io.Writer) {
	fmt.Fprintln(output, `PageLode extracts Markdown, discovers page data endpoints, and finds emails.

Usage:
  pagelode <URL>
  pagelode --json <URL>
  pagelode extract <URL>
  pagelode discover [--profile <name>] [--verbose] [--wait-ms 1500] <URL>
  pagelode discover --har <capture.har>
  pagelode emails [--max-pages 20] [--profile <name>] [--verbose] <URL>
  pagelode profile login <name> <URL>
  pagelode serve
  pagelode version

Examples:
  pagelode example.com
  pagelode https://example.com/article
  pagelode --json example.com`)
}
