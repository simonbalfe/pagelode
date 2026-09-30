package chromefetch_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/simonbalfe/pagelode/internal/chromefetch"
	"github.com/simonbalfe/pagelode/internal/discovery"
	"github.com/simonbalfe/pagelode/internal/page"
)

func TestBrowserCapture(t *testing.T) {
	if os.Getenv("PAGELODE_BROWSER_TESTS") != "1" {
		t.Skip("set PAGELODE_BROWSER_TESTS=1 to run Chromium integration")
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set("Set-Cookie", "fixture=secret-cookie; Path=/")
		_, err := w.Write([]byte(`<html><head><title>Discovery fixture</title></head><body><main><h1>Listings</h1><p>This fixture loads structured listing data from REST and GraphQL endpoints so PageLode can discover where the displayed information originates.</p></main><script>fetch('/api/listings?page=1&token=secret-query', {headers:{Authorization:'Bearer secret-header'}});fetch('/graphql', {method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({operationName:'GetListings',query:'query GetListings { listings { id } }',variables:{after:'private-cursor'}})});</script></body></html>`))
		if err != nil {
			t.Error(err)
		}
	})
	mux.HandleFunc("/api/listings", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{"items": []map[string]any{{"id": 1, "price": 120000}}, "nextCursor": "secret-response"}); err != nil {
			t.Error(err)
		}
	})
	mux.HandleFunc("/graphql", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, err := w.Write([]byte(`{"data":{"listings":[{"id":1}]}}`))
		if err != nil {
			t.Error(err)
		}
	})
	server := httptest.NewServer(mux)
	defer server.Close()
	engine, err := chromefetch.New("")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := engine.Close(); err != nil {
			t.Error(err)
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	document, err := engine.Capture(ctx, server.URL, page.Session{}, page.CaptureOptions{WaitMS: 500})
	if err != nil {
		t.Fatal(err)
	}
	if len(document.Session.Cookies) == 0 {
		t.Error("browser session omitted the fixture cookie")
	}
	report := discovery.Analyze(server.URL, document)
	if report.Summary.DataRequests != 2 {
		t.Fatalf("report = %+v, want 2 data requests", report)
	}
	graphql := false
	for _, endpoint := range report.Endpoints {
		if endpoint.Operation == "GetListings" {
			graphql = true
		}
		if endpoint.Path == "/api/listings" && len(endpoint.Auth) == 0 {
			t.Error("listing endpoint has no auth signal")
		}
	}
	if !graphql {
		t.Errorf("endpoints = %+v, want GetListings operation", report.Endpoints)
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"secret-query", "secret-header", "secret-response", "private-cursor", "secret-cookie"} {
		if strings.Contains(string(encoded), secret) {
			t.Errorf("report exposes %q", secret)
		}
	}
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := engine.Capture(ctx, server.URL, page.Session{}, page.CaptureOptions{WaitMS: 100}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
}
