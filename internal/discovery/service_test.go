package discovery

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/simonbalfe/pagelode/internal/limit"
	"github.com/simonbalfe/pagelode/internal/memory"
	"github.com/simonbalfe/pagelode/internal/page"
	"github.com/simonbalfe/pagelode/internal/profile"
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

func TestDiscoverEscalatesBlockedPage(t *testing.T) {
	chrome := &captureStub{document: page.Document{Provider: page.ProviderChromedp, StatusCode: 403, HTML: `<title>Just a moment</title><form id="challenge-form"></form>`, Type: page.ContentHTML, Traffic: &page.Capture{}}}
	patch := &captureStub{document: page.Document{Provider: page.ProviderPatchright, StatusCode: 200, HTML: `<main><h1>Listings</h1><p>This page contains useful listing content and enough text for the classifier to accept the rendered document successfully.</p></main>`, Type: page.ContentHTML, Traffic: &page.Capture{Entries: []page.Exchange{{URL: "https://example.com/api", Method: "GET", MIMEType: "application/json", ResponseBody: `{"items":[]}`}}}}}
	service := New(chrome, patch, memory.NewRoutes(time.Hour, nil), limit.New(1, 1), true)
	report, err := service.Discover(context.Background(), Request{URL: "example.com", Verbose: true})
	if err != nil {
		t.Fatal(err)
	}
	if report.Page.Provider != page.ProviderPatchright || len(report.Attempts) != 2 || chrome.calls != 1 || patch.calls != 1 {
		t.Errorf("report = %+v, calls = %d/%d", report, chrome.calls, patch.calls)
	}
}

func TestDiscoverHonorsProtectedRoute(t *testing.T) {
	chrome := &captureStub{}
	patch := &captureStub{document: page.Document{Provider: page.ProviderPatchright, Traffic: &page.Capture{}}}
	service := New(chrome, patch, memory.NewRoutes(time.Hour, []string{"example.com"}), limit.New(1, 1), true)
	_, err := service.Discover(context.Background(), Request{URL: "https://example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if chrome.calls != 0 || patch.calls != 1 {
		t.Errorf("calls = %d/%d, want 0/1", chrome.calls, patch.calls)
	}
}

func TestDiscoverPropagatesCancellation(t *testing.T) {
	service := New(&captureStub{err: context.DeadlineExceeded}, nil, memory.NewRoutes(time.Hour, nil), limit.New(1, 1), true)
	_, err := service.Discover(context.Background(), Request{URL: "https://example.com"})
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
	service := New(nil, nil, nil, nil, false)
	report, err := service.Discover(context.Background(), Request{HAR: &har})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Endpoints) != 1 || !containsField(report.Endpoints[0].ResponseSchema, "$.items[].id", "number") {
		t.Errorf("report = %+v", report)
	}
}

func TestValidateDiscoveryRequest(t *testing.T) {
	for _, request := range []Request{{URL: "file:///etc/passwd"}, {URL: "https://user:password@example.com"}, {URL: "https://example.com", WaitMS: -1}, {URL: "https://example.com", WaitMS: 10001}, {HAR: &HAR{}}, {URL: "example.com", Profile: "../account"}, {HAR: &HAR{Log: HARLog{Entries: []HAREntry{{}}}}, Profile: "account"}} {
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
	service := New(nil, nil, nil, nil, false)
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

func TestDiscoveryProfileUsesPatchright(t *testing.T) {
	root := t.TempDir()
	directory, err := profile.Directory(root, "account", true)
	if err != nil {
		t.Fatal(err)
	}
	chrome := &captureStub{}
	patch := &captureStub{document: page.Document{Provider: page.ProviderPatchright, Traffic: &page.Capture{}}}
	service := New(chrome, patch, memory.NewRoutes(time.Hour, nil), limit.New(1, 1), true).WithProfiles(root)
	_, err = service.Discover(context.Background(), Request{URL: "example.com", Profile: "account"})
	if err != nil {
		t.Fatal(err)
	}
	if chrome.calls != 0 || patch.calls != 1 || patch.options.ProfileDirectory != directory {
		t.Errorf("calls=%d/%d profile=%q", chrome.calls, patch.calls, patch.options.ProfileDirectory)
	}
	_, err = service.Discover(context.Background(), Request{URL: "example.com", Profile: "missing"})
	if err == nil || patch.calls != 1 {
		t.Errorf("missing profile error=%v calls=%d", err, patch.calls)
	}
}
