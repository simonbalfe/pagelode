package discovery

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/simonbalfe/pagelode/internal/page"
)

func TestAnalyzeTraffic(t *testing.T) {
	entries := []page.Exchange{
		{URL: "https://example.com/api/listings/12?page=1&token=private-token", Method: "GET", ResourceType: "Fetch", Status: 200, MIMEType: "application/json", RequestHeaders: map[string]string{"Authorization": "Bearer private-token", "Cookie": "session=private-cookie"}, ResponseBody: `{"items":[{"id":12,"price":125000,"password":"private-password"}],"nextCursor":"private-cursor"}`},
		{URL: "https://example.com/api/listings/13?page=2", Method: "GET", ResourceType: "Fetch", Status: 200, MIMEType: "application/json", ResponseBody: `{"items":[{"id":13,"price":null}]}`},
		{URL: "https://example.com/graphql", Method: "POST", ResourceType: "Fetch", Status: 200, MIMEType: "application/json", RequestBody: `{"operationName":"GetListings","query":"query GetListings { listings { id } }","variables":{"after":"private-cursor"}}`, ResponseBody: `{"data":{"listings":[{"id":12}]}}`},
		{URL: "https://www.google-analytics.com/collect?token=private-token", Method: "POST", ResourceType: "Fetch", Status: 200, MIMEType: "application/json"},
	}
	report := Analyze("https://example.com/?token=private-token", page.Document{FinalURL: "https://example.com/", Provider: page.ProviderChromedp, Traffic: &page.Capture{Entries: entries}, HTML: `<script type="application/ld+json">{"name":"Example"}</script>`})
	if report.Summary.EndpointCount != 2 || report.Summary.DataRequests != 3 || report.Summary.NoiseRequests != 1 {
		t.Fatalf("summary = %+v, want 2 endpoints, 3 data, 1 noise", report.Summary)
	}
	endpoint := report.Endpoints[0]
	if endpoint.Path != "/api/listings/{id}" || endpoint.Calls != 2 || len(endpoint.Auth) != 3 {
		t.Errorf("endpoint = %+v, want grouped listing endpoint with auth", endpoint)
	}
	if !containsField(endpoint.ResponseSchema, "$.items[].price", "number") || !containsField(endpoint.ResponseSchema, "$.items[].price", "null") {
		t.Errorf("response schema = %+v, want mixed number/null price", endpoint.ResponseSchema)
	}
	if report.Endpoints[1].Protocol != "graphql" || report.Endpoints[1].Operation != "GetListings" {
		t.Errorf("GraphQL endpoint = %+v", report.Endpoints[1])
	}
	if !hasPagination(report.Pagination, "response_body", "$.nextCursor") || !hasPagination(report.Pagination, "request_body", "$.variables.after") {
		t.Errorf("pagination = %+v", report.Pagination)
	}
	if len(report.DataSources) < 3 {
		t.Errorf("data sources = %+v, want API arrays and embedded JSON", report.DataSources)
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"private-token", "private-cookie", "private-password", "private-cursor"} {
		if strings.Contains(string(encoded), secret) {
			t.Errorf("report exposes %q", secret)
		}
	}
}

func TestAnalyzeCoverageWarnings(t *testing.T) {
	report := Analyze("https://example.com", page.Document{Traffic: &page.Capture{Dropped: 2, Entries: []page.Exchange{{URL: "https://example.com/api", Method: "GET", ResourceType: "Fetch", Truncated: true, Error: "response body unavailable"}}}})
	if report.Summary.Truncated != 1 || report.Summary.Incomplete != 1 || report.Summary.Dropped != 2 {
		t.Errorf("summary = %+v", report.Summary)
	}
	if len(report.Warnings) < 3 {
		t.Errorf("warnings = %v, want explicit capture gaps", report.Warnings)
	}
}

func containsField(fields []Field, path, kind string) bool {
	for _, field := range fields {
		if field.Path == path {
			for _, observed := range field.Types {
				if observed == kind {
					return true
				}
			}
		}
	}
	return false
}
func hasPagination(signals []Signal, location, name string) bool {
	for _, signal := range signals {
		if signal.Location == location && signal.Name == name {
			return true
		}
	}
	return false
}

func TestGoogleAnalyticsRegionalHostIsNoise(t *testing.T) {
	report := Analyze("https://example.com", page.Document{Traffic: &page.Capture{Entries: []page.Exchange{
		{URL: "https://region1.analytics.google.com/g/collect", Method: "POST", ResourceType: "Fetch", Status: 204},
	}}})
	if report.Summary.DataRequests != 0 || report.Summary.NoiseRequests != 1 || len(report.Endpoints) != 0 {
		t.Errorf("summary = %+v, endpoints = %+v, want one noise request and no endpoints", report.Summary, report.Endpoints)
	}
}
