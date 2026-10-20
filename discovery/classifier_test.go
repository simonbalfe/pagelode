package discovery

import (
	"encoding/json"
	"os"
	"reflect"
	"sort"
	"testing"

	"github.com/simonbalfe/pagelode/page"
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

const soapEnvelope = `<?xml version="1.0"?><soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/"><soap:Body><GetPrice/></soap:Body></soap:Envelope>`

func TestScoreEntryStructuredAPIs(t *testing.T) {
	for _, item := range []struct {
		name  string
		entry page.Exchange
		data  bool
	}{
		{"SOAP action header", page.Exchange{Method: "POST", URL: "https://example.com/Service.asmx", MIMEType: "text/xml", RequestHeaders: map[string]string{"SOAPAction": `"urn:GetPrice"`, "Content-Type": "text/xml; charset=utf-8"}}, true},
		{"empty SOAP action header", page.Exchange{Method: "POST", URL: "https://example.com/Service.asmx", MIMEType: "text/xml", RequestHeaders: map[string]string{"soapaction": ""}}, true},
		{"SOAP 1.2 request type", page.Exchange{Method: "POST", URL: "https://example.com/ws", RequestHeaders: map[string]string{"Content-Type": "application/soap+xml; charset=utf-8"}}, true},
		{"SOAP 1.2 response type", page.Exchange{Method: "POST", URL: "https://example.com/ws", MIMEType: "application/soap+xml"}, true},
		{"text/xml envelope request", page.Exchange{Method: "POST", URL: "https://example.com/ws", MIMEType: "text/xml", RequestBody: soapEnvelope}, true},
		{"text/xml envelope response", page.Exchange{Method: "POST", URL: "https://example.com/ws", MIMEType: "text/xml", ResponseBody: soapEnvelope}, true},
		{"text/xml without envelope", page.Exchange{Method: "GET", URL: "https://example.com/feed", MIMEType: "text/xml", ResponseBody: `<rss><channel/></rss>`}, false},
		{"envelope in non-XML body", page.Exchange{Method: "POST", URL: "https://example.com/ws", MIMEType: "text/plain", RequestBody: soapEnvelope}, false},
		{"unrelated xml envelope", page.Exchange{Method: "POST", URL: "https://example.com/ws", MIMEType: "text/xml", RequestBody: `<Envelope><to>a</to></Envelope>`}, false},
		{"SOAP on blocked host", page.Exchange{Method: "POST", URL: "https://api.segment.io/Service.asmx", MIMEType: "text/xml", RequestHeaders: map[string]string{"SOAPAction": "x"}}, false},
		{"gRPC-web response", page.Exchange{Method: "POST", URL: "https://example.com/pkg.Service/Method", MIMEType: "application/grpc-web+proto"}, true},
		{"gRPC-web text request", page.Exchange{Method: "POST", URL: "https://example.com/pkg.Service/Method", RequestHeaders: map[string]string{"content-type": "application/grpc-web-text"}}, true},
		{"gRPC response", page.Exchange{Method: "POST", URL: "https://example.com/pkg.Service/Method", MIMEType: "application/grpc"}, true},
		{"application/graphql request", page.Exchange{Method: "POST", URL: "https://example.com/query", RequestHeaders: map[string]string{"Content-Type": "application/graphql"}, RequestBody: `{ viewer { id } }`}, true},
		{"XML POST", page.Exchange{Method: "POST", URL: "https://example.com/submit", MIMEType: "application/xml"}, true},
		{"XML on API path", page.Exchange{Method: "GET", URL: "https://example.com/api/items", MIMEType: "application/xml"}, true},
		{"XML on rest path", page.Exchange{Method: "GET", URL: "https://example.com/rest/items", MIMEType: "application/xml"}, true},
		{"XML GET plain page", page.Exchange{Method: "GET", URL: "https://example.com/sitemap.xml", MIMEType: "application/xml"}, false},
		{"multipart POST", page.Exchange{Method: "POST", URL: "https://example.com/upload", RequestHeaders: map[string]string{"Content-Type": "multipart/form-data; boundary=x"}}, true},
		{"multipart GET", page.Exchange{Method: "GET", URL: "https://example.com/upload", RequestHeaders: map[string]string{"Content-Type": "multipart/form-data; boundary=x"}}, false},
		{"multipart POST HTML response", page.Exchange{Method: "POST", URL: "https://example.com/upload", MIMEType: "text/html", RequestHeaders: map[string]string{"Content-Type": "multipart/form-data"}}, false},
	} {
		t.Run(item.name, func(t *testing.T) {
			score := scoreEntry(item.entry, defaultBlocklist())
			if got := score > 0; got != item.data {
				t.Errorf("score = %d, data = %v, want data = %v", score, got, item.data)
			}
		})
	}
}

func TestDetectProtocol(t *testing.T) {
	jsonType := map[string]string{"Content-Type": "application/json"}
	for _, item := range []struct {
		name      string
		entry     page.Exchange
		protocol  string
		operation string
	}{
		{"SOAPAction", page.Exchange{Method: "POST", URL: "https://example.com/Service.asmx", MIMEType: "text/xml", RequestHeaders: map[string]string{"SOAPAction": "urn:x"}}, "soap", ""},
		{"empty SOAPAction", page.Exchange{Method: "POST", URL: "https://example.com/Service.asmx", RequestHeaders: map[string]string{"SOAPAction": ""}}, "soap", ""},
		{"soap+xml request", page.Exchange{Method: "POST", URL: "https://example.com/ws", RequestHeaders: map[string]string{"content-type": `application/soap+xml; action="urn:x"`}}, "soap", ""},
		{"soap+xml response", page.Exchange{Method: "POST", URL: "https://example.com/ws", MIMEType: "application/soap+xml"}, "soap", ""},
		{"text/xml envelope", page.Exchange{Method: "POST", URL: "https://example.com/ws", MIMEType: "text/xml", RequestBody: soapEnvelope}, "soap", ""},
		{"SOAP beats graphql path", page.Exchange{Method: "POST", URL: "https://example.com/graphql", RequestHeaders: map[string]string{"SOAPAction": "x"}}, "soap", ""},
		{"plain xml is not SOAP", page.Exchange{Method: "GET", URL: "https://example.com/feed", MIMEType: "text/xml", ResponseBody: `<rss/>`}, "http", ""},
		{"graphql path", page.Exchange{Method: "POST", URL: "https://example.com/graphql", MIMEType: "application/json"}, "graphql", ""},
		{"operationName body", page.Exchange{Method: "POST", URL: "https://example.com/api", RequestBody: `{"operationName":"GetUsers"}`}, "graphql", "GetUsers"},
		{"query document body", page.Exchange{Method: "POST", URL: "https://example.com/api", RequestBody: `{"query":"query Q { a }"}`}, "graphql", ""},
		{"shorthand query document body", page.Exchange{Method: "POST", URL: "https://example.com/api", RequestBody: `{"query":" { a }"}`}, "graphql", ""},
		{"search query body is not graphql", page.Exchange{Method: "POST", URL: "https://example.com/api/search", MIMEType: "application/json", RequestBody: `{"query":"running shoes"}`}, "rest_json", ""},
		{"search phrase starting with query", page.Exchange{Method: "POST", URL: "https://example.com/api/search", MIMEType: "application/json", RequestBody: `{"query":"query for shoes"}`}, "rest_json", ""},
		{"application/graphql request", page.Exchange{Method: "POST", URL: "https://example.com/query", RequestHeaders: map[string]string{"Content-Type": "application/graphql"}, RequestBody: `{ viewer { id } }`}, "graphql", ""},
		{"application/graphql response", page.Exchange{Method: "POST", URL: "https://example.com/query", MIMEType: "application/graphql; charset=utf-8"}, "graphql", ""},
		{"GET query string document", page.Exchange{Method: "GET", URL: "https://example.com/api/q?query=%7B%20viewer%20%7B%20id%20%7D%20%7D", MIMEType: "application/json"}, "graphql", ""},
		{"GET query string query keyword", page.Exchange{Method: "GET", URL: "https://example.com/api/q?query=query%20Q%20%7B%20a%20%7D&variables=%7B%7D", MIMEType: "application/json"}, "graphql", ""},
		{"GET query string operationName", page.Exchange{Method: "GET", URL: "https://example.com/api/q?operationName=GetUsers", MIMEType: "application/json"}, "graphql", "GetUsers"},
		{"GET search query is not graphql", page.Exchange{Method: "GET", URL: "https://example.com/api/search?query=shoes", MIMEType: "application/json"}, "rest_json", ""},
		{"persisted body", page.Exchange{Method: "POST", URL: "https://example.com/api", RequestBody: `{"operationName":"Q","extensions":{"persistedQuery":{"version":1,"sha256Hash":"abc"}}}`}, "graphql_persisted_query", "Q"},
		{"persisted body without name", page.Exchange{Method: "POST", URL: "https://example.com/api", RequestBody: `{"extensions":{"persistedQuery":{"version":1,"sha256Hash":"abc"}}}`}, "graphql_persisted_query", ""},
		{"persisted GET", page.Exchange{Method: "GET", URL: "https://example.com/api/q?operationName=Q&extensions=%7B%22persistedQuery%22%3A%7B%22version%22%3A1%2C%22sha256Hash%22%3A%22abc%22%7D%7D", MIMEType: "application/json"}, "graphql_persisted_query", "Q"},
		{"extensions without persisted query", page.Exchange{Method: "GET", URL: "https://example.com/api/items?extensions=%7B%22a%22%3A1%7D", MIMEType: "application/json"}, "rest_json", ""},
		{"named batch", page.Exchange{Method: "POST", URL: "https://example.com/graphql", RequestBody: `[{"operationName":"A"},{"operationName":"B"},{"operationName":"A"}]`}, "graphql_batch", "A,B"},
		{"anonymous query batch", page.Exchange{Method: "POST", URL: "https://example.com/api", RequestBody: `[{"query":"{ a }"},{"query":"{ b }"}]`}, "graphql_batch", ""},
		{"persisted batch", page.Exchange{Method: "POST", URL: "https://example.com/api", RequestBody: `[{"extensions":{"persistedQuery":{"sha256Hash":"a"}}},{"extensions":{"persistedQuery":{"sha256Hash":"b"}}}]`}, "graphql_batch", ""},
		{"non-graphql array body", page.Exchange{Method: "POST", URL: "https://example.com/api/items", MIMEType: "application/json", RequestBody: `[{"id":1},{"id":2}]`}, "rest_json", ""},
		{"empty array body", page.Exchange{Method: "POST", URL: "https://example.com/api/items", MIMEType: "application/json", RequestBody: `[]`}, "rest_json", ""},
		{"json-rpc", page.Exchange{Method: "POST", URL: "https://example.com/rpc", RequestHeaders: jsonType, RequestBody: `{"jsonrpc":"2.0","method":"eth_call","id":1}`}, "json_rpc", ""},
		{"json-rpc with query field", page.Exchange{Method: "POST", URL: "https://example.com/rpc", RequestBody: `{"jsonrpc":"2.0","method":"search","params":{"query":"{ a }"},"id":1}`}, "json_rpc", ""},
		{"json-rpc batch", page.Exchange{Method: "POST", URL: "https://example.com/rpc", RequestBody: `[{"jsonrpc":"2.0","method":"a","id":1},{"jsonrpc":"2.0","method":"b","id":2}]`}, "json_rpc", ""},
		{"grpc-web response", page.Exchange{Method: "POST", URL: "https://example.com/pkg.Service/Method", MIMEType: "application/grpc-web+proto"}, "grpc_web", ""},
		{"grpc-web text request", page.Exchange{Method: "POST", URL: "https://example.com/pkg.Service/Method", RequestHeaders: map[string]string{"Content-Type": "application/grpc-web-text"}}, "grpc_web", ""},
		{"x-grpc-web header", page.Exchange{Method: "POST", URL: "https://example.com/pkg.Service/Method", RequestHeaders: map[string]string{"X-Grpc-Web": "1"}}, "grpc_web", ""},
		{"batchexecute", page.Exchange{Method: "POST", URL: "https://example.com/_/batchexecute"}, "google_batchexecute", ""},
		{"trpc", page.Exchange{Method: "GET", URL: "https://example.com/api/trpc/list", MIMEType: "application/json"}, "trpc", ""},
		{"sse", page.Exchange{Method: "GET", URL: "https://example.com/events", MIMEType: "text/event-stream"}, "sse", ""},
		{"rest json", page.Exchange{Method: "GET", URL: "https://example.com/api/items", MIMEType: "application/json"}, "rest_json", ""},
		{"html", page.Exchange{Method: "GET", URL: "https://example.com/", MIMEType: "text/html"}, "html", ""},
	} {
		t.Run(item.name, func(t *testing.T) {
			protocol, operation := detectProtocol(item.entry)
			if protocol != item.protocol || operation != item.operation {
				t.Errorf("detectProtocol = (%q, %q), want (%q, %q)", protocol, operation, item.protocol, item.operation)
			}
		})
	}
}

func TestAuthSignals(t *testing.T) {
	for _, item := range []struct {
		name    string
		headers map[string]string
		url     string
		want    []string
	}{
		{"bearer", map[string]string{"Authorization": "Bearer abc"}, "https://example.com/", []string{"bearer"}},
		{"basic keeps generic label and names scheme", map[string]string{"authorization": "Basic dXNlcjpwYXNz"}, "https://example.com/", []string{"authorization", "basic"}},
		{"digest", map[string]string{"Authorization": `Digest username="u"`}, "https://example.com/", []string{"authorization", "digest"}},
		{"schemeless value", map[string]string{"Authorization": "abc"}, "https://example.com/", []string{"authorization"}},
		{"unknown scheme", map[string]string{"Authorization": "Token abc"}, "https://example.com/", []string{"authorization"}},
		{"cookie and csrf", map[string]string{"Cookie": "a=b", "X-CSRF-Token": "t"}, "https://example.com/", []string{"cookies", "csrf"}},
		{"none", nil, "https://example.com/", []string{}},
	} {
		t.Run(item.name, func(t *testing.T) {
			got := authSignals(page.Exchange{URL: item.url, RequestHeaders: item.headers})
			sort.Strings(got)
			if !reflect.DeepEqual(got, item.want) {
				t.Errorf("authSignals = %v, want %v", got, item.want)
			}
		})
	}
}

func TestAnalyzeStructuredProtocols(t *testing.T) {
	report := Analyze("https://example.com", page.Document{Traffic: &page.Capture{Entries: []page.Exchange{
		{URL: "https://example.com/Service.asmx", Method: "POST", Status: 200, MIMEType: "text/xml", RequestHeaders: map[string]string{"SOAPAction": "urn:x", "Authorization": "Basic dXNlcjpwYXNz"}, RequestBody: soapEnvelope, ResponseBody: soapEnvelope},
		{URL: "https://example.com/pkg.Service/Method", Method: "POST", Status: 200, MIMEType: "application/grpc-web+proto", RequestHeaders: map[string]string{"Content-Type": "application/grpc-web+proto"}},
	}}})
	if report.Summary.DataRequests != 2 || len(report.Endpoints) != 2 {
		t.Fatalf("summary = %+v, endpoints = %d, want both requests kept as data endpoints", report.Summary, len(report.Endpoints))
	}
	if report.Endpoints[0].Protocol != "soap" || report.Endpoints[1].Protocol != "grpc_web" {
		t.Errorf("protocols = %q, %q, want soap and grpc_web", report.Endpoints[0].Protocol, report.Endpoints[1].Protocol)
	}
	if !reflect.DeepEqual(report.Endpoints[0].Auth, []string{"authorization", "basic"}) && !reflect.DeepEqual(report.Endpoints[0].Auth, []string{"basic", "authorization"}) {
		t.Errorf("auth = %v, want authorization and basic", report.Endpoints[0].Auth)
	}
}
