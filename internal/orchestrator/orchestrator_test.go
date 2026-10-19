package orchestrator

import (
	"context"
	"testing"
	"time"

	"github.com/simonbalfe/pagelode/internal/memory"
	"github.com/simonbalfe/pagelode/internal/page"
)

type stubFetcher struct {
	document page.Document
	err      error
	calls    int
}

func (s *stubFetcher) Fetch(context.Context, string, page.Session) (page.Document, error) {
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
			httpFetcher := &stubFetcher{document: page.Document{Provider: page.ProviderTLS, StatusCode: 200, FinalURL: "https://example.com", Type: page.ContentHTML, HTML: test.html}}
			browserFetcher := &stubFetcher{document: usableDocument(page.ProviderPatchright)}
			routes := memory.NewRoutes(time.Hour, nil)
			service := New(httpFetcher, browserFetcher, nil, routes, memory.NewClearances(time.Hour))

			result, err := service.Extract(context.Background(), "https://example.com")
			if err != nil {
				t.Fatalf("Extract() error = %v", err)
			}
			if result.Provider != page.ProviderPatchright || browserFetcher.calls != 1 {
				t.Errorf("Extract() provider = %q with %d browser calls, want patchright once", result.Provider, browserFetcher.calls)
			}
			if got, ok := routes.Preferred("example.com"); !ok || got != page.ProviderPatchright {
				t.Errorf("route after browser success = %q, %v; want patchright remembered", got, ok)
			}
		})
	}
}

func TestProtectedDomainStartsWithTheBrowser(t *testing.T) {
	t.Parallel()

	httpFetcher := &stubFetcher{document: usableDocument(page.ProviderTLS)}
	browserFetcher := &stubFetcher{document: usableDocument(page.ProviderPatchright)}
	service := testService(httpFetcher, browserFetcher, nil, []string{"crunchbase.com"})

	result, err := service.Extract(context.Background(), "https://www.crunchbase.com/organization/example")
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if result.Provider != page.ProviderPatchright {
		t.Errorf("Extract() provider = %q, want patchright", result.Provider)
	}
	if httpFetcher.calls != 0 {
		t.Errorf("HTTP calls = %d, want zero", httpFetcher.calls)
	}
}

func TestBlockedBrowserEscalatesToCapSolver(t *testing.T) {
	t.Parallel()

	httpFetcher := &stubFetcher{document: usableDocument(page.ProviderTLS)}
	browserFetcher := &stubFetcher{document: page.Document{
		Provider: page.ProviderPatchright, StatusCode: 403, FinalURL: "https://example.com", Type: page.ContentHTML,
		HTML: `<html><title>Just a moment</title><body><form id="challenge-form"></form></body></html>`,
	}}
	capSolverFetcher := &stubFetcher{document: usableDocument(page.ProviderCapSolver)}
	service := testService(httpFetcher, browserFetcher, capSolverFetcher, []string{"example.com"})

	result, err := service.Extract(context.Background(), "https://example.com")
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if result.Provider != page.ProviderCapSolver {
		t.Errorf("provider = %q, want capsolver", result.Provider)
	}
	if httpFetcher.calls != 0 {
		t.Errorf("HTTP calls = %d, want 0", httpFetcher.calls)
	}
	if browserFetcher.calls != 1 || capSolverFetcher.calls != 1 {
		t.Errorf("calls = browser:%d capsolver:%d, want 1 each", browserFetcher.calls, capSolverFetcher.calls)
	}
}

func testService(httpFetcher Fetcher, browserFetcher Fetcher, capSolverFetcher Fetcher, protected []string) *Service {
	return New(httpFetcher, browserFetcher, capSolverFetcher, memory.NewRoutes(time.Hour, protected), memory.NewClearances(time.Hour))
}

func usableDocument(provider page.Provider) page.Document {
	return page.Document{
		Provider:   provider,
		StatusCode: 200,
		FinalURL:   "https://example.com/",
		Title:      "Example",
		Type:       page.ContentHTML,
		HTML:       `<html><head><title>Example</title></head><body><main><h1>Example</h1><p>This page contains enough readable content for the extraction pipeline to accept it successfully without another browser escalation.</p></main></body></html>`,
	}
}
