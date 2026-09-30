package discovery

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/simonbalfe/pagelode/internal/page"
)

func TestPrintingPressEndpointParity(t *testing.T) {
	var fixture struct {
		Cases []struct {
			Entry struct {
				Method         string            `json:"method"`
				URL            string            `json:"url"`
				MIMEType       string            `json:"response_content_type"`
				RequestHeaders map[string]string `json:"request_headers"`
				ResponseBody   string            `json:"response_body"`
			} `json:"entry"`
			Score  int    `json:"score"`
			Path   string `json:"path"`
			Host   string `json:"host"`
			Method string `json:"method"`
		} `json:"cases"`
		Groups []struct {
			Host   string `json:"host"`
			Method string `json:"method"`
			Path   string `json:"path"`
			Calls  int    `json:"calls"`
		} `json:"groups"`
	}
	data, err := os.ReadFile("testdata/printing-press-endpoints.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	entries := make([]page.Exchange, 0, len(fixture.Cases))
	for i, item := range fixture.Cases {
		entry := page.Exchange{Method: item.Entry.Method, URL: item.Entry.URL, MIMEType: item.Entry.MIMEType, RequestHeaders: item.Entry.RequestHeaders, ResponseBody: item.Entry.ResponseBody}
		entries = append(entries, entry)
		if got := scoreEntry(entry, defaultBlocklist()); got != item.Score {
			t.Errorf("case %d %s: API score = %d, want %d", i, entry.URL, got, item.Score)
		}
		if got := normalizeEntryPath(entry.URL); got != item.Path {
			t.Errorf("case %d %s: path = %q, want %q", i, entry.URL, got, item.Path)
		}
	}
	report := Analyze("https://example.com", page.Document{Traffic: &page.Capture{Entries: entries}})
	if len(report.Endpoints) != len(fixture.Groups) {
		t.Fatalf("groups = %d, want %d", len(report.Endpoints), len(fixture.Groups))
	}
	for i, want := range fixture.Groups {
		got := report.Endpoints[i]
		if got.Host != want.Host || got.Method != want.Method || got.Path != want.Path || got.Calls != want.Calls {
			t.Errorf("group %d = %+v, want %+v", i, got, want)
		}
	}
	for i, evidence := range report.Evidence {
		want := "noise"
		if fixture.Cases[i].Score > 0 {
			want = "data"
		}
		if evidence.Classification != want {
			t.Errorf("case %d classification = %s, want %s", i, evidence.Classification, want)
		}
	}
}

func TestGraphQLOperationsShareTransportEndpoint(t *testing.T) {
	report := Analyze("https://example.com", page.Document{Traffic: &page.Capture{Entries: []page.Exchange{
		{URL: "https://example.com/graphql", Method: "POST", MIMEType: "application/json", RequestBody: `{"operationName":"GetUsers"}`},
		{URL: "https://example.com/graphql", Method: "POST", MIMEType: "application/json", RequestBody: `{"operationName":"GetProjects"}`},
	}}})
	if len(report.Endpoints) != 1 {
		t.Fatalf("endpoints = %d, want 1", len(report.Endpoints))
	}
	endpoint := report.Endpoints[0]
	if endpoint.Calls != 2 || endpoint.Operation != "" || len(endpoint.Operations) != 2 {
		t.Errorf("endpoint = %+v, want both operations on one transport endpoint", endpoint)
	}
}
