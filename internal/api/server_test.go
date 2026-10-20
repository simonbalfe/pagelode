package api

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/simonbalfe/pagelode/pkg/fetch"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type apiFetcher struct{}

func (apiFetcher) Fetch(context.Context, string, fetch.Session) (fetch.Document, error) {
	return fetch.Document{
		Provider:   fetch.ProviderTLS,
		StatusCode: 200,
		FinalURL:   "https://example.com/",
		Type:       fetch.ContentHTML,
		HTML:       `<html><head><title>Example</title></head><body><main><h1>Example</h1><p>This is enough useful page content for the API extraction test to produce a successful result.</p></main></body></html>`,
	}, nil
}

func TestExtract(t *testing.T) {
	t.Parallel()

	routes := fetch.NewRoutes(time.Hour, nil)
	extractor := fetch.NewService(apiFetcher{}, nil, nil, routes, fetch.NewClearances(time.Hour))
	server := New(extractor, nil, fetch.NewLimiter(2, 2), routes, time.Second, slog.New(slog.NewTextHandler(io.Discard, nil)))

	request := httptest.NewRequest(http.MethodPost, "/extract", bytes.NewBufferString(`{"url":"https://example.com"}`))
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", response.Code, response.Body.String())
	}
	var result fetch.Result
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if result.Outcome != "ok" || result.Provider != fetch.ProviderTLS {
		t.Errorf("result = outcome %q provider %q, want ok/tls", result.Outcome, result.Provider)
	}
}
