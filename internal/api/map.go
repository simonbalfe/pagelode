package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/simonbalfe/pagelode/pkg/fetch"
	"github.com/simonbalfe/pagelode/pkg/sitemap"
)

func (s *Server) WithMap(mapper *sitemap.Service) *Server {
	s.mapper = mapper
	return s
}

func (s *Server) siteMap(response http.ResponseWriter, request *http.Request) {
	request.Body = http.MaxBytesReader(response, request.Body, maximumRequestBytes)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	var input sitemap.Request
	if err := decoder.Decode(&input); err != nil {
		writeJSON(response, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
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
	if s.mapper == nil {
		writeJSON(response, http.StatusServiceUnavailable, map[string]string{"error": "site mapper is not configured"})
		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), s.timeout)
	defer cancel()
	report, err := s.mapper.Map(ctx, input)
	if err != nil {
		switch {
		case errors.Is(err, fetch.ErrSaturated):
			response.Header().Set("Retry-After", "1")
			writeJSON(response, http.StatusTooManyRequests, map[string]string{"error": "PageLode queue is full"})
		case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
			writeJSON(response, http.StatusRequestTimeout, map[string]string{"error": "site map timed out"})
		default:
			writeJSON(response, http.StatusBadGateway, map[string]string{"error": "site map could not start; check the URL"})
		}
		return
	}
	writeJSON(response, http.StatusOK, report)
}
