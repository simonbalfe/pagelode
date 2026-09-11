package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/simonbalfe/pagelode/internal/limit"
	"github.com/simonbalfe/pagelode/internal/memory"
	"github.com/simonbalfe/pagelode/internal/orchestrator"
	"github.com/simonbalfe/pagelode/internal/page"
)

type apiFetcher struct{}

func (apiFetcher) Fetch(context.Context, string, page.Session) (page.Document, error) {
	return page.Document{
		Provider:   page.ProviderTLS,
		StatusCode: 200,
		FinalURL:   "https://example.com/",
		Type:       page.ContentHTML,
		HTML:       `<html><head><title>Example</title></head><body><main><h1>Example</h1><p>This is enough useful page content for the API extraction test to produce a successful result.</p></main></body></html>`,
	}, nil
}

func TestExtract(t *testing.T) {
	t.Parallel()

	routes := memory.NewRoutes(time.Hour, nil)
	browserLimiter := limit.New(1, 1)
	extractor := orchestrator.New(apiFetcher{}, nil, nil, routes, browserLimiter, false)
	server := New(extractor, limit.New(2, 2), browserLimiter, routes, time.Second, slog.New(slog.NewTextHandler(io.Discard, nil)))

	request := httptest.NewRequest(http.MethodPost, "/extract", bytes.NewBufferString(`{"url":"https://example.com"}`))
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", response.Code, response.Body.String())
	}
	var result orchestrator.Result
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if result.Outcome != "ok" || result.Provider != page.ProviderTLS {
		t.Errorf("result = outcome %q provider %q, want ok/tls", result.Outcome, result.Provider)
	}
}
