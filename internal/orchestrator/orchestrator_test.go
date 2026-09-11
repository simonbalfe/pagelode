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

func TestBlockedHTTPSkipsRod(t *testing.T) {
	t.Parallel()

	httpFetcher := &stubFetcher{document: page.Document{
		Provider: page.ProviderTLS, StatusCode: 403, FinalURL: "https://example.com", Type: page.ContentHTML,
		HTML: `<html><title>Just a moment</title><body><form id="challenge-form" action="?__cf_chl_f_tk=x"></form></body></html>`,
	}}
	rodFetcher := &stubFetcher{}
	patchrightFetcher := &stubFetcher{document: usableDocument(page.ProviderPatchright)}
	service := testService(httpFetcher, rodFetcher, patchrightFetcher, nil)

	result, err := service.Extract(context.Background(), "https://example.com")
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if result.Provider != page.ProviderPatchright {
		t.Errorf("Extract() provider = %q, want patchright", result.Provider)
	}
	if rodFetcher.calls != 0 {
		t.Errorf("Rod calls = %d, want 0", rodFetcher.calls)
	}
}

func TestJavaScriptShellUsesRod(t *testing.T) {
	t.Parallel()

	httpFetcher := &stubFetcher{document: page.Document{
		Provider: page.ProviderTLS, StatusCode: 200, FinalURL: "https://example.com", Type: page.ContentHTML,
		HTML: `<html><body><div id="root"></div><script src="/1.js"></script><script src="/2.js"></script><script src="/3.js"></script><script src="/4.js"></script></body></html>`,
	}}
	rodFetcher := &stubFetcher{document: usableDocument(page.ProviderRod)}
	patchrightFetcher := &stubFetcher{document: usableDocument(page.ProviderPatchright)}
	service := testService(httpFetcher, rodFetcher, patchrightFetcher, nil)

	result, err := service.Extract(context.Background(), "https://example.com")
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if result.Provider != page.ProviderRod {
		t.Errorf("Extract() provider = %q, want rod", result.Provider)
	}
	if patchrightFetcher.calls != 0 {
		t.Errorf("Patchright calls = %d, want 0", patchrightFetcher.calls)
	}
}

func TestProtectedDomainStartsWithPatchright(t *testing.T) {
	t.Parallel()

	httpFetcher := &stubFetcher{document: usableDocument(page.ProviderTLS)}
	rodFetcher := &stubFetcher{document: usableDocument(page.ProviderRod)}
	patchrightFetcher := &stubFetcher{document: usableDocument(page.ProviderPatchright)}
	service := testService(httpFetcher, rodFetcher, patchrightFetcher, []string{"crunchbase.com"})

	result, err := service.Extract(context.Background(), "https://www.crunchbase.com/organization/example")
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if result.Provider != page.ProviderPatchright {
		t.Errorf("Extract() provider = %q, want patchright", result.Provider)
	}
	if httpFetcher.calls != 0 || rodFetcher.calls != 0 {
		t.Errorf("lower-tier calls = http:%d rod:%d, want zero", httpFetcher.calls, rodFetcher.calls)
	}
}

func testService(httpFetcher Fetcher, rodFetcher Fetcher, patchrightFetcher Fetcher, protected []string) *Service {
	return New(
		httpFetcher,
		rodFetcher,
		patchrightFetcher,
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
