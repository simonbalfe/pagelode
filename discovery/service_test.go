package discovery

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/simonbalfe/pagelode/page"
)

type captureStub struct {
	document page.Document
	err      error
	calls    int
	options  page.CaptureOptions
}

func (s *captureStub) Capture(_ context.Context, _ string, _ page.Session, options page.CaptureOptions) (page.Document, error) {
	s.calls++
	s.options = options
	return s.document, s.err
}

func TestDiscoverReportsPageStillBlockedInBrowser(t *testing.T) {
	browser := &captureStub{document: page.Document{Provider: page.ProviderPatchright, StatusCode: 403, HTML: `<title>Just a moment</title><form id="challenge-form"></form>`, Type: page.ContentHTML, Traffic: &page.Capture{}}}
	report, err := New(browser).Discover(context.Background(), Request{URL: "example.com", Verbose: true})
	if err != nil {
		t.Fatal(err)
	}
	if report.Outcome != "blocked" || len(report.Attempts) != 1 || browser.calls != 1 {
		t.Errorf("report outcome = %q, attempts = %d, calls = %d; want blocked after one capture", report.Outcome, len(report.Attempts), browser.calls)
	}
}

func TestDiscoverPropagatesCancellation(t *testing.T) {
	_, err := New(&captureStub{err: context.DeadlineExceeded}).Discover(context.Background(), Request{URL: "https://example.com"})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("error = %v, want deadline exceeded", err)
	}
}

func TestHARDiscovery(t *testing.T) {
	var har HAR
	var item HAREntry
	item.Request.URL = "https://example.com/api?api_key=private"
	item.Request.Method = "GET"
	item.Response.Status = 200
	item.Response.Content.MIMEType = "application/json"
	item.Response.Content.Encoding = "base64"
	item.Response.Content.Text = base64.StdEncoding.EncodeToString([]byte(`{"items":[{"id":1}],"nextCursor":"secret"}`))
	har.Log.Entries = []HAREntry{item}
	service := New(nil)
	report, err := service.Discover(context.Background(), Request{HAR: &har})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Endpoints) != 1 || !containsField(report.Endpoints[0].ResponseSchema, "$.items[].id", "number") {
		t.Errorf("report = %+v", report)
	}
}

func TestValidateDiscoveryRequest(t *testing.T) {
	for _, request := range []Request{{URL: "file:///etc/passwd"}, {URL: "https://user:password@example.com"}, {URL: "https://example.com", WaitMS: -1}, {URL: "https://example.com", WaitMS: 10001}, {HAR: &HAR{}}, {URL: "example.com", Profile: "../account"}, {HAR: &HAR{Log: HARLog{Entries: []HAREntry{{}}}}, Profile: "account"}, {HAR: &HAR{Log: HARLog{Entries: []HAREntry{{}}}}, Stealth: true}} {
		if _, err := request.Validate(); err == nil {
			t.Errorf("Validate(%+v) succeeded", request)
		}
	}
}

func TestDiscoveryVerbosity(t *testing.T) {
	var item HAREntry
	item.Request.URL = "https://example.com/api"
	item.Request.Method = "GET"
	item.Response.Content.MIMEType = "application/json"
	item.Response.Content.Text = `{"items":[{"id":1}]}`
	har := HAR{Log: HARLog{Entries: []HAREntry{item}}}
	service := New(nil)
	for _, verbose := range []bool{false, true} {
		report, err := service.Discover(context.Background(), Request{HAR: &har, Verbose: verbose})
		if err != nil {
			t.Fatal(err)
		}
		data, err := json.Marshal(report)
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.Contains(string(data), `"evidence"`); got != verbose {
			t.Errorf("verbose=%t evidence present=%t: %s", verbose, got, data)
		}
		if len(report.Endpoints) != 1 || len(report.Endpoints[0].ResponseSchema) == 0 {
			t.Errorf("verbose=%t lost endpoint information", verbose)
		}
	}
}

func TestDiscoveryPassesProfile(t *testing.T) {
	browser := &captureStub{document: page.Document{Provider: page.ProviderPatchright, Traffic: &page.Capture{}}}
	if _, err := New(browser).Discover(context.Background(), Request{URL: "example.com", Profile: "account"}); err != nil {
		t.Fatal(err)
	}
	if browser.calls != 1 || browser.options.Profile != "account" {
		t.Errorf("calls=%d profile=%q, want one capture with profile account", browser.calls, browser.options.Profile)
	}
}

func TestDiscoveryStealthRequiresProxy(t *testing.T) {
	browser := &captureStub{document: page.Document{Provider: page.ProviderPatchright, Traffic: &page.Capture{}}}
	service := New(browser)
	if _, err := service.Discover(context.Background(), Request{URL: "example.com", Stealth: true}); !errors.Is(err, ErrStealthProxy) {
		t.Fatalf("without proxy error = %v, want ErrStealthProxy", err)
	}
	if browser.calls != 0 {
		t.Fatalf("without proxy calls = %d, want 0", browser.calls)
	}
	if _, err := service.WithProxy(true).Discover(context.Background(), Request{URL: "example.com", Stealth: true}); err != nil {
		t.Fatal(err)
	}
	if browser.calls != 1 {
		t.Errorf("calls = %d, want 1", browser.calls)
	}
}
