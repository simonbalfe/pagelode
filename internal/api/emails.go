package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/simonbalfe/pagelode/internal/emails"
	"github.com/simonbalfe/pagelode/internal/limit"
)

func (s *Server) WithEmails(finder *emails.Service) *Server {
	s.emailFinder = finder
	return s
}

func (s *Server) emails(response http.ResponseWriter, request *http.Request) {
	request.Body = http.MaxBytesReader(response, request.Body, maximumRequestBytes)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	var input emails.Request
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
	if s.emailFinder == nil {
		writeJSON(response, http.StatusServiceUnavailable, map[string]string{"error": "email finder is not configured"})
		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), s.timeout)
	defer cancel()
	report, err := s.emailFinder.Find(ctx, input)
	if err != nil {
		switch {
		case errors.Is(err, limit.ErrSaturated):
			response.Header().Set("Retry-After", "1")
			writeJSON(response, http.StatusTooManyRequests, map[string]string{"error": "PageLode queue is full"})
		case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
			writeJSON(response, http.StatusRequestTimeout, map[string]string{"error": "email search timed out"})
		default:
			writeJSON(response, http.StatusBadGateway, map[string]string{"error": "email search could not start; check the URL and saved profile"})
		}
		return
	}
	writeJSON(response, http.StatusOK, report)
}
