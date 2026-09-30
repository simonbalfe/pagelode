package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/simonbalfe/pagelode/internal/discovery"
	"github.com/simonbalfe/pagelode/internal/limit"
)

func (s *Server) discover(response http.ResponseWriter, request *http.Request) {
	if s.discoverer == nil {
		writeJSON(response, http.StatusServiceUnavailable, map[string]string{"error": "discovery unavailable"})
		return
	}
	request.Body = http.MaxBytesReader(response, request.Body, 4<<20)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	var input discovery.Request
	if err := decoder.Decode(&input); err != nil {
		writeJSON(response, http.StatusBadRequest, map[string]string{"error": "invalid discovery request"})
		return
	}
	if err := ensureEOF(decoder); err != nil {
		writeJSON(response, http.StatusBadRequest, map[string]string{"error": "request body must contain one JSON object"})
		return
	}
	input, err := input.Validate()
	if err != nil {
		writeJSON(response, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), s.timeout)
	defer cancel()
	var report discovery.Report
	err = s.extractLimiter.Run(ctx, func() error { var err error; report, err = s.discoverer.Discover(ctx, input); return err })
	if err != nil {
		switch {
		case errors.Is(err, limit.ErrSaturated):
			response.Header().Set("Retry-After", "1")
			writeJSON(response, http.StatusTooManyRequests, map[string]string{"error": "PageLode queue is full"})
		case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
			writeJSON(response, http.StatusRequestTimeout, map[string]string{"error": "discovery timed out"})
		default:
			writeJSON(response, http.StatusBadGateway, map[string]string{"error": "browser discovery failed"})
		}
		return
	}
	s.logger.Info("discover completed", "host", hostname(input.URL), "outcome", report.Outcome, "requests", report.Summary.Requests, "endpoints", report.Summary.EndpointCount)
	writeJSON(response, http.StatusOK, report)
}
