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

	"github.com/simonbalfe/pagelode/internal/discovery"
	"github.com/simonbalfe/pagelode/internal/limit"
	"github.com/simonbalfe/pagelode/internal/memory"
	"github.com/simonbalfe/pagelode/internal/page"
)

type apiCapturer struct{ calls int }

func (c *apiCapturer) Capture(context.Context, string, page.Session, page.CaptureOptions) (page.Document, error) {
	c.calls++
	return page.Document{Provider: page.ProviderChromedp, Type: page.ContentHTML, Traffic: &page.Capture{Entries: []page.Exchange{{URL: "https://example.com/api/listings", Method: "GET", MIMEType: "application/json", ResponseBody: `{"items":[{"id":1}]}`}}}}, nil
}

func discoveryServer(capturer discovery.Capturer) *Server {
	routes := memory.NewRoutes(time.Hour, nil)
	browserLimiter := limit.New(1, 1)
	service := discovery.New(capturer, nil, routes, browserLimiter, true)
	return New(nil, service, limit.New(2, 2), browserLimiter, routes, time.Second, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func TestDiscoveryEndpoint(t *testing.T) {
	capture := &apiCapturer{}
	server := discoveryServer(capture)
	request := httptest.NewRequest(http.MethodPost, "/discover", bytes.NewBufferString(`{"url":"https://example.com","waitMs":100}`))
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var report discovery.Report
	if err := json.Unmarshal(response.Body.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.Outcome != "ok" || len(report.Endpoints) != 1 || capture.calls != 1 {
		t.Errorf("report = %+v, capture calls = %d", report, capture.calls)
	}
}

func TestDiscoveryRejectsInvalidInput(t *testing.T) {
	for _, body := range []string{`{"url":"file:///etc/passwd"}`, `{"url":"https://example.com","waitMs":10001}`, `{"url":"https://example.com","unexpected":true}`, `{"url":"https://example.com"} {}`, `{"har":{"log":{"entries":[]}}}`} {
		capture := &apiCapturer{}
		server := discoveryServer(capture)
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/discover", bytes.NewBufferString(body)))
		if response.Code != http.StatusBadRequest || capture.calls != 0 {
			t.Errorf("body %s: status = %d calls = %d, want 400/0", body, response.Code, capture.calls)
		}
	}
}

func TestDiscoverHARAcceptsStandardMetadata(t *testing.T) {
	capture := &apiCapturer{}
	server := discoveryServer(capture)
	body := `{"har":{"log":{"version":"1.2","creator":{"name":"Chrome"},"entries":[{"time":1,"request":{"url":"https://example.com/api","method":"GET","httpVersion":"HTTP/1.1"},"response":{"status":200,"content":{"mimeType":"application/json","text":"{\"items\":[]}"}}}]}}}`
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/discover", bytes.NewBufferString(body)))
	if response.Code != http.StatusOK || capture.calls != 0 {
		t.Errorf("status = %d calls = %d body = %s, want 200/0", response.Code, capture.calls, response.Body.String())
	}
}

func TestDiscoveryAPIVerbosity(t *testing.T) {
	for _, verbose := range []bool{false, true} {
		body, err := json.Marshal(discovery.Request{URL: "example.com", Verbose: verbose})
		if err != nil {
			t.Fatal(err)
		}
		response := httptest.NewRecorder()
		discoveryServer(&apiCapturer{}).Handler().ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/discover", bytes.NewReader(body)))
		if response.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
		if got := bytes.Contains(response.Body.Bytes(), []byte(`"evidence"`)); got != verbose {
			t.Errorf("verbose=%t evidence present=%t", verbose, got)
		}
	}
}
