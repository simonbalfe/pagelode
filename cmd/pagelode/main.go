package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/simonbalfe/pagelode/internal/api"
	"github.com/simonbalfe/pagelode/internal/config"
	"github.com/simonbalfe/pagelode/internal/httpfetch"
	"github.com/simonbalfe/pagelode/internal/limit"
	"github.com/simonbalfe/pagelode/internal/memory"
	"github.com/simonbalfe/pagelode/internal/orchestrator"
	"github.com/simonbalfe/pagelode/internal/patchright"
	"github.com/simonbalfe/pagelode/internal/rodfetch"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	configuration, err := config.Load()
	if err != nil {
		logger.Error("load configuration", "error", err)
		os.Exit(1)
	}

	patchrightClient, err := patchright.New(configuration.PatchrightCommand, []string{configuration.PatchrightWorker}, configuration.ProxyURL)
	if err != nil {
		logger.Error("configure Patchright", "error", err)
		os.Exit(1)
	}
	rod, err := rodfetch.New(configuration.ProxyURL)
	if err != nil {
		logger.Error("configure Rod", "error", err)
		os.Exit(1)
	}
	routes := memory.NewRoutes(configuration.RouteTTL, configuration.ProtectedDomains)
	browserLimiter := limit.New(configuration.BrowserConcurrency, configuration.MaxWaiting)
	extractLimiter := limit.New(configuration.MaxConcurrency, configuration.MaxWaiting)
	extractor := orchestrator.New(
		httpfetch.New(configuration.ProxyURL),
		rod,
		patchrightClient,
		routes,
		browserLimiter,
		configuration.RodEnabled,
	)
	apiServer := api.New(extractor, extractLimiter, browserLimiter, routes, configuration.RequestTimeout, logger)
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
		if err := rod.Close(); err != nil {
			logger.Error("close Rod", "error", err)
		}
		if err := patchrightClient.Close(); err != nil {
			logger.Error("close Patchright", "error", err)
		}
	}()

	logger.Info("pagelode listening", "address", configuration.Address, "rod", configuration.RodEnabled, "protected_domains", configuration.ProtectedDomains)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("serve", "error", err)
		os.Exit(1)
	}
}
