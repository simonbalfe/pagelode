package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/simonbalfe/pagelode/internal/limit"
	"github.com/simonbalfe/pagelode/internal/memory"
	"github.com/simonbalfe/pagelode/internal/orchestrator"
)

const maximumRequestBytes = 16 << 10

type Server struct {
	extractor      *orchestrator.Service
	extractLimiter *limit.Limiter
	browserLimiter *limit.Limiter
	routes         *memory.Routes
	timeout        time.Duration
	logger         *slog.Logger
}

func New(extractor *orchestrator.Service, extractLimiter *limit.Limiter, browserLimiter *limit.Limiter, routes *memory.Routes, timeout time.Duration, logger *slog.Logger) *Server {
	return &Server{
		extractor:      extractor,
		extractLimiter: extractLimiter,
		browserLimiter: browserLimiter,
		routes:         routes,
		timeout:        timeout,
		logger:         logger,
	}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.health)
	mux.HandleFunc("POST /extract", s.extract)
	return mux
}

func (s *Server) health(response http.ResponseWriter, _ *http.Request) {
	writeJSON(response, http.StatusOK, map[string]any{
		"ok":            true,
		"service":       "pagelode",
		"engines":       []string{"tls", "rod", "patchright"},
		"learnedRoutes": s.routes.Size(),
		"limits": map[string]any{
			"extraction": s.extractLimiter.Snapshot(),
			"browser":    s.browserLimiter.Snapshot(),
		},
	})
}

func (s *Server) extract(response http.ResponseWriter, request *http.Request) {
	request.Body = http.MaxBytesReader(response, request.Body, maximumRequestBytes)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	var input struct {
		URL string `json:"url"`
	}
	if err := decoder.Decode(&input); err != nil {
		writeJSON(response, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	if err := ensureEOF(decoder); err != nil {
		writeJSON(response, http.StatusBadRequest, map[string]string{"error": "request body must contain one JSON object"})
		return
	}

	ctx, cancel := context.WithTimeout(request.Context(), s.timeout)
	defer cancel()
	var (
		result orchestrator.Result
		err    error
	)
	started := time.Now()
	limitErr := s.extractLimiter.Run(ctx, func() error {
		result, err = s.extractor.Extract(ctx, input.URL)
		return err
	})
	if limitErr != nil {
		switch {
		case errors.Is(limitErr, limit.ErrSaturated):
			response.Header().Set("Retry-After", "1")
			writeJSON(response, http.StatusTooManyRequests, map[string]string{"error": "PageLode queue is full"})
		case errors.Is(limitErr, context.Canceled), errors.Is(limitErr, context.DeadlineExceeded):
			writeJSON(response, http.StatusRequestTimeout, map[string]string{"error": "extraction timed out"})
		default:
			writeJSON(response, http.StatusBadRequest, map[string]string{"error": limitErr.Error()})
		}
		return
	}

	s.logger.Info("extract completed",
		"outcome", result.Outcome,
		"provider", result.Provider,
		"attempts", len(result.Attempts),
		"duration_ms", time.Since(started).Milliseconds(),
		"host", hostname(input.URL),
	)
	writeJSON(response, http.StatusOK, result)
}

func ensureEOF(decoder *json.Decoder) error {
	var extra any
	err := decoder.Decode(&extra)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err == nil {
		return errors.New("extra JSON value")
	}
	return fmt.Errorf("decode trailing JSON: %w", err)
}

func writeJSON(response http.ResponseWriter, status int, value any) {
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(status)
	if err := json.NewEncoder(response).Encode(value); err != nil {
		slog.Error("write response", "error", err)
	}
}

func hostname(value string) string {
	if !strings.Contains(value, "://") {
		value = "https://" + value
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return "invalid"
	}
	return parsed.Hostname()
}
