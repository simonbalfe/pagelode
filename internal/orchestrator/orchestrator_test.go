package orchestrator

import (
	"context"
	"testing"
	"time"

	"github.com/simonbalfe/pagelode/internal/limit"
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

func TestBlockedHTTPSkipsChromedp(t *testing.T) {
	t.Parallel()

	httpFetcher := &stubFetcher{document: page.Document{
		Provider: page.ProviderTLS, StatusCode: 403, FinalURL: "https://example.com", Type: page.ContentHTML,
		HTML: `<html><title>Just a moment</title><body><form id="challenge-form" action="?__cf_chl_f_tk=x"></form></body></html>`,
	}}
	chromedpFetcher := &stubFetcher{}
	patchrightFetcher := &stubFetcher{document: usableDocument(page.ProviderPatchright)}
	service := testService(httpFetcher, chromedpFetcher, patchrightFetcher, nil, nil)

	result, err := service.Extract(context.Background(), "https://example.com")
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if result.Provider != page.ProviderPatchright {
		t.Errorf("Extract() provider = %q, want patchright", result.Provider)
	}
	if chromedpFetcher.calls != 0 {
		t.Errorf("Chromedp calls = %d, want 0", chromedpFetcher.calls)
	}
}

func TestJavaScriptShellUsesChromedp(t *testing.T) {
	t.Parallel()

	httpFetcher := &stubFetcher{document: page.Document{
		Provider: page.ProviderTLS, StatusCode: 200, FinalURL: "https://example.com", Type: page.ContentHTML,
		HTML: `<html><body><div id="root"></div><script src="/1.js"></script><script src="/2.js"></script><script src="/3.js"></script><script src="/4.js"></script></body></html>`,
	}}
	chromedpFetcher := &stubFetcher{document: usableDocument(page.ProviderChromedp)}
	patchrightFetcher := &stubFetcher{document: usableDocument(page.ProviderPatchright)}
	service := testService(httpFetcher, chromedpFetcher, patchrightFetcher, nil, nil)

	result, err := service.Extract(context.Background(), "https://example.com")
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if result.Provider != page.ProviderChromedp {
		t.Errorf("Extract() provider = %q, want chromedp", result.Provider)
	}
	if patchrightFetcher.calls != 0 {
		t.Errorf("Patchright calls = %d, want 0", patchrightFetcher.calls)
	}
}

func TestProtectedDomainStartsWithPatchright(t *testing.T) {
	t.Parallel()

	httpFetcher := &stubFetcher{document: usableDocument(page.ProviderTLS)}
	chromedpFetcher := &stubFetcher{document: usableDocument(page.ProviderChromedp)}
	patchrightFetcher := &stubFetcher{document: usableDocument(page.ProviderPatchright)}
	service := testService(httpFetcher, chromedpFetcher, patchrightFetcher, nil, []string{"crunchbase.com"})

	result, err := service.Extract(context.Background(), "https://www.crunchbase.com/organization/example")
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if result.Provider != page.ProviderPatchright {
		t.Errorf("Extract() provider = %q, want patchright", result.Provider)
	}
	if httpFetcher.calls != 0 || chromedpFetcher.calls != 0 {
		t.Errorf("lower-tier calls = http:%d chromedp:%d, want zero", httpFetcher.calls, chromedpFetcher.calls)
	}
}

func TestBlockedPatchrightEscalatesToCapSolver(t *testing.T) {
	t.Parallel()

	httpFetcher := &stubFetcher{document: usableDocument(page.ProviderTLS)}
	patchrightFetcher := &stubFetcher{document: page.Document{
		Provider: page.ProviderPatchright, StatusCode: 403, FinalURL: "https://example.com", Type: page.ContentHTML,
		HTML: `<html><title>Just a moment</title><body><form id="challenge-form"></form></body></html>`,
	}}
	capSolverFetcher := &stubFetcher{document: usableDocument(page.ProviderCapSolver)}
	service := testService(httpFetcher, nil, patchrightFetcher, capSolverFetcher, []string{"example.com"})

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
	if patchrightFetcher.calls != 1 || capSolverFetcher.calls != 1 {
		t.Errorf("calls = patchright:%d capsolver:%d, want 1 each", patchrightFetcher.calls, capSolverFetcher.calls)
	}
}

func testService(httpFetcher Fetcher, chromedpFetcher Fetcher, patchrightFetcher Fetcher, capSolverFetcher Fetcher, protected []string) *Service {
	return New(
		httpFetcher,
		chromedpFetcher,
		patchrightFetcher,
		capSolverFetcher,
		memory.NewRoutes(time.Hour, protected),
		limit.New(2, 2),
		true,
	)
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
