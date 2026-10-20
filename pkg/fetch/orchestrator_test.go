package fetch

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

type stubFetcher struct {
	document Document
	err      error
	calls    int
}

func (s *stubFetcher) Fetch(context.Context, string, Session) (Document, error) {
	s.calls++
	return s.document, s.err
}

func TestHTTPResultsNeedingABrowserUseTheBrowserService(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name string
		html string
	}{
		{name: "blocked", html: `<html><title>Just a moment</title><body><form id="challenge-form" action="?__cf_chl_f_tk=x"></form></body></html>`},
		{name: "javascript shell", html: `<html><body><div id="root"></div><script src="/1.js"></script><script src="/2.js"></script><script src="/3.js"></script><script src="/4.js"></script></body></html>`},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			httpFetcher := &stubFetcher{document: Document{Provider: ProviderTLS, StatusCode: 200, FinalURL: "https://example.com", Type: ContentHTML, HTML: test.html}}
			browserFetcher := &stubFetcher{document: usableDocument(ProviderPatchright)}
			routes := NewRoutes(time.Hour, nil)
			service := NewService(httpFetcher, browserFetcher, nil, routes, NewClearances(time.Hour))

			result, err := service.Extract(context.Background(), "https://example.com")
			if err != nil {
				t.Fatalf("Extract() error = %v", err)
			}
			if result.Provider != ProviderPatchright || browserFetcher.calls != 1 {
				t.Errorf("Extract() provider = %q with %d browser calls, want patchright once", result.Provider, browserFetcher.calls)
			}
			if got, ok := routes.Preferred("example.com"); !ok || got != ProviderPatchright {
				t.Errorf("route after browser success = %q, %v; want patchright remembered", got, ok)
			}
		})
	}
}

func TestProtectedDomainStartsWithTheBrowser(t *testing.T) {
	t.Parallel()

	httpFetcher := &stubFetcher{document: usableDocument(ProviderTLS)}
	browserFetcher := &stubFetcher{document: usableDocument(ProviderPatchright)}
	service := testService(httpFetcher, browserFetcher, nil, []string{"crunchbase.com"})

	result, err := service.Extract(context.Background(), "https://www.crunchbase.com/organization/example")
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if result.Provider != ProviderPatchright {
		t.Errorf("Extract() provider = %q, want patchright", result.Provider)
	}
	if httpFetcher.calls != 0 {
		t.Errorf("HTTP calls = %d, want zero", httpFetcher.calls)
	}
}

func TestBlockedBrowserEscalatesToCapSolver(t *testing.T) {
	t.Parallel()

	httpFetcher := &stubFetcher{document: usableDocument(ProviderTLS)}
	browserFetcher := &stubFetcher{document: Document{
		Provider: ProviderPatchright, StatusCode: 403, FinalURL: "https://example.com", Type: ContentHTML,
		HTML: `<html><title>Just a moment</title><body><form id="challenge-form"></form></body></html>`,
	}}
	capSolverFetcher := &stubFetcher{document: usableDocument(ProviderCapSolver)}
	service := testService(httpFetcher, browserFetcher, capSolverFetcher, []string{"example.com"})

	result, err := service.Extract(context.Background(), "https://example.com")
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if result.Provider != ProviderCapSolver {
		t.Errorf("provider = %q, want capsolver", result.Provider)
	}
	if httpFetcher.calls != 0 {
		t.Errorf("HTTP calls = %d, want 0", httpFetcher.calls)
	}
	if browserFetcher.calls != 1 || capSolverFetcher.calls != 1 {
		t.Errorf("calls = browser:%d capsolver:%d, want 1 each", browserFetcher.calls, capSolverFetcher.calls)
	}
}

func TestExtractHTTPHandsBrowserWorkToTheBrowserStage(t *testing.T) {
	t.Parallel()
	shell := Document{Provider: ProviderTLS, StatusCode: 200, FinalURL: "https://example.com", Type: ContentHTML,
		HTML: `<html><body><div id="root"></div><script src="/1.js"></script><script src="/2.js"></script><script src="/3.js"></script><script src="/4.js"></script></body></html>`}
	for _, test := range []struct {
		name        string
		http        Document
		protected   []string
		wantOutcome string
		wantHTTP    int
	}{
		{name: "usable over HTTP", http: usableDocument(ProviderTLS), wantOutcome: "ok", wantHTTP: 1},
		{name: "javascript shell", http: shell, wantOutcome: OutcomeNeedsBrowser, wantHTTP: 1},
		{name: "dead page", http: Document{Provider: ProviderTLS, StatusCode: 404, FinalURL: "https://example.com", Type: ContentHTML}, wantOutcome: "dead", wantHTTP: 1},
		{name: "non-HTML response the browser cannot extract", http: Document{Provider: ProviderTLS, StatusCode: 200, FinalURL: "https://example.com/report.pdf", Type: ContentPDF}, wantOutcome: "failed", wantHTTP: 1},
		{name: "browser route remembered", http: usableDocument(ProviderTLS), protected: []string{"example.com"}, wantOutcome: OutcomeNeedsBrowser, wantHTTP: 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			httpFetcher := &stubFetcher{document: test.http}
			browserFetcher := &stubFetcher{document: usableDocument(ProviderPatchright)}
			capSolverFetcher := &stubFetcher{document: usableDocument(ProviderCapSolver)}
			service := testService(httpFetcher, browserFetcher, capSolverFetcher, test.protected)

			result, err := service.ExtractHTTP(context.Background(), "https://example.com")
			if err != nil {
				t.Fatalf("ExtractHTTP() error = %v", err)
			}
			if result.Outcome != test.wantOutcome {
				t.Errorf("ExtractHTTP() outcome = %q, want %q", result.Outcome, test.wantOutcome)
			}
			if httpFetcher.calls != test.wantHTTP || browserFetcher.calls != 0 || capSolverFetcher.calls != 0 {
				t.Errorf("calls = http:%d browser:%d capsolver:%d, want http:%d and no browser or CapSolver", httpFetcher.calls, browserFetcher.calls, capSolverFetcher.calls, test.wantHTTP)
			}
		})
	}
}

func TestExtractBrowserReturnsCapacityErrorsInsteadOfFailing(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name string
		err  error
	}{
		{name: "saturated", err: fmt.Errorf("patchright: saturated: %w", ErrSaturated)},
		{name: "unreachable", err: fmt.Errorf("patchright: connection refused: %w", ErrUnavailable)},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			browserFetcher := &stubFetcher{err: test.err}
			capSolverFetcher := &stubFetcher{document: usableDocument(ProviderCapSolver)}
			routes := NewRoutes(time.Hour, nil)
			service := NewService(&stubFetcher{}, browserFetcher, capSolverFetcher, routes, NewClearances(time.Hour))

			_, err := service.ExtractBrowser(context.Background(), "https://example.com")
			if !errors.Is(err, test.err) {
				t.Fatalf("ExtractBrowser() error = %v, want it to wrap %v", err, test.err)
			}
			if capSolverFetcher.calls != 0 {
				t.Errorf("CapSolver calls = %d, want 0 while the browser has no capacity", capSolverFetcher.calls)
			}
			if _, ok := routes.Preferred("example.com"); ok {
				t.Error("route remembered after a capacity error, want none")
			}
		})
	}
}

func TestExtractStillFallsBackToCapSolverWhenTheBrowserIsFull(t *testing.T) {
	t.Parallel()
	shell := Document{Provider: ProviderTLS, StatusCode: 403, FinalURL: "https://example.com", Type: ContentHTML,
		HTML: `<html><title>Just a moment</title><body><form id="challenge-form"></form></body></html>`}
	capSolverFetcher := &stubFetcher{document: usableDocument(ProviderCapSolver)}
	service := testService(&stubFetcher{document: shell}, &stubFetcher{err: ErrSaturated}, capSolverFetcher, nil)

	result, err := service.Extract(context.Background(), "https://example.com")
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if result.Provider != ProviderCapSolver || capSolverFetcher.calls != 1 {
		t.Errorf("Extract() provider = %q with %d CapSolver calls, want CapSolver once", result.Provider, capSolverFetcher.calls)
	}
}

func testService(httpFetcher Fetcher, browserFetcher Fetcher, capSolverFetcher Fetcher, protected []string) *Service {
	return NewService(httpFetcher, browserFetcher, capSolverFetcher, NewRoutes(time.Hour, protected), NewClearances(time.Hour))
}

func usableDocument(provider Provider) Document {
	return Document{
		Provider:   provider,
		StatusCode: 200,
		FinalURL:   "https://example.com/",
		Title:      "Example",
		Type:       ContentHTML,
		HTML:       `<html><head><title>Example</title></head><body><main><h1>Example</h1><p>This page contains enough readable content for the extraction pipeline to accept it successfully without another browser escalation.</p></main></body></html>`,
	}
}
