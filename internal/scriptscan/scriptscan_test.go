package scriptscan

import (
	"fmt"
	"net/url"
	"reflect"
	"strings"
	"testing"
)

func TestEndpoints(t *testing.T) {
	tests := []struct {
		name   string
		source string
		want   []string
	}{
		{"empty", "", nil},
		{"no endpoints", `var a = "hello"; var b = 'world/x';`, nil},
		{"double quotes", `var u = "/api/v1/users";`, []string{"/api/v1/users"}},
		{"fetch single quotes", `fetch('/graphql', {method: 'POST'})`, []string{"/graphql"}},
		{"axios backtick without interpolation", "axios.get(`/items`)", []string{"/items"}},
		{"backtick interpolation excluded", "axios.get(`/items/${id}`)", nil},
		{"protocol relative excluded", `load("//cdn.example.com/x.js")`, nil},
		{"mime values excluded", `h["Content-Type"] = "application/json"; a = "/application/json"; b = "/text/html"`, nil},
		{"mime-like prefix kept when real path", `get("/image/upload")`, []string{"/image/upload"}},
		{"spaces excluded", `x = "/a b"`, nil},
		{"braces excluded", `x = "/a/{id}"`, nil},
		{"backslash excluded", `{"u": "\/api\/v1"}`, nil},
		{"bare slash excluded", `s.split("/"); p = '/'`, nil},
		{"too long excluded", fmt.Sprintf(`x = "/%s"`, strings.Repeat("a", 200)), nil},
		{"max length kept", fmt.Sprintf(`x = "/%s"`, strings.Repeat("a", 199)), []string{"/" + strings.Repeat("a", 199)}},
		{
			name:   "static assets excluded",
			source: `a("/logo.png"); a("/a.JPG"); a("/x.jpeg"); a("/x.gif"); a("/i.svg"); a("/i.webp"); a("/favicon.ico"); a("/s.css"); a("/f.woff"); a("/f.woff2"); a("/f.ttf"); a("/m.js.map"); a("/v.mp4"); a("/logo.png?v=2")`,
			want:   nil,
		},
		{"js and html kept", `a("/app.js"); a("/page.html"); a("/v1.2/users")`, []string{"/app.js", "/page.html", "/v1.2/users"}},
		{"dot in directory not extension", `a("/files.png/list")`, []string{"/files.png/list"}},
		{"query and fragment kept", `a("/search?q=1&p=2"); a("/docs#intro")`, []string{"/search?q=1&p=2", "/docs#intro"}},
		{"duplicates removed in order", `a("/b"); a('/a'); a("/b"); a("/a")`, []string{"/b", "/a"}},
		{
			name:   "json source",
			source: `{"self": "/api/me", "next": "/api/page/2", "name": "x"}`,
			want:   []string{"/api/me", "/api/page/2"},
		},
		{"other quote inside literal", `a("/it's/fine")`, []string{"/it's/fine"}},
		{"unterminated ignored", `a("/api/open`, nil},
		{"multiline literal ignored", "a(\"/api/one\n/api/two\")", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Endpoints(tt.source)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("Endpoints() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestEndpointsLimit(t *testing.T) {
	var b strings.Builder
	for i := range 600 {
		fmt.Fprintf(&b, "a(\"/e/%d\");", i)
	}
	got := Endpoints(b.String())
	if len(got) != 500 {
		t.Fatalf("len = %d, want 500", len(got))
	}
	if got[0] != "/e/0" || got[499] != "/e/499" {
		t.Fatalf("unexpected first/last: %q %q", got[0], got[499])
	}
}

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func TestScriptURLs(t *testing.T) {
	tests := []struct {
		name string
		html string
		base string
		want []string
	}{
		{"empty", "", "https://example.com/", nil},
		{"no scripts", "<p>hi</p>", "https://example.com/", nil},
		{"inline script ignored", "<script>var a=1</script>", "https://example.com/", nil},
		{
			name: "relative and absolute resolved",
			html: `<script src="/static/app.js"></script><script src="chunk.js"></script><script src="https://example.com/a.js"></script>`,
			base: "https://example.com/dir/page",
			want: []string{"https://example.com/static/app.js", "https://example.com/dir/chunk.js", "https://example.com/a.js"},
		},
		{
			name: "third party excluded",
			html: `<script src="https://cdn.other.com/x.js"></script><script src="//www.google-analytics.com/a.js"></script><script src="/own.js"></script>`,
			base: "https://example.com/",
			want: []string{"https://example.com/own.js"},
		},
		{
			name: "www stripped on either side",
			html: `<script src="https://www.example.com/a.js"></script><script src="https://example.com/b.js"></script>`,
			base: "https://example.com/",
			want: []string{"https://www.example.com/a.js", "https://example.com/b.js"},
		},
		{
			name: "www base matches bare host",
			html: `<script src="https://example.com/b.js"></script>`,
			base: "https://www.example.com/",
			want: []string{"https://example.com/b.js"},
		},
		{"subdomain is a different host", `<script src="https://cdn.example.com/x.js"></script>`, "https://example.com/", nil},
		{
			name: "protocol relative same host",
			html: `<script src="//example.com/p.js"></script>`,
			base: "https://example.com/",
			want: []string{"https://example.com/p.js"},
		},
		{
			name: "duplicates removed",
			html: `<script src="/a.js"></script><script src="/a.js"></script><script src="/a.js#x"></script>`,
			base: "https://example.com/",
			want: []string{"https://example.com/a.js"},
		},
		{"empty and blank src ignored", `<script src=""></script><script src="  "></script>`, "https://example.com/", nil},
		{"non http scheme ignored", `<script src="javascript:alert(1)"></script><script src="data:text/javascript,1"></script>`, "https://example.com/", nil},
		{"malformed src ignored", `<script src="http://[::1"></script><script src="/ok.js"></script>`, "https://example.com/", []string{"https://example.com/ok.js"}},
		{"query preserved", `<script src="/a.js?v=3"></script>`, "https://example.com/", []string{"https://example.com/a.js?v=3"}},
		{"port ignored in host match", `<script src="https://example.com:8443/a.js"></script>`, "https://example.com/", []string{"https://example.com:8443/a.js"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ScriptURLs(tt.html, mustURL(t, tt.base))
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("ScriptURLs() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestScriptURLsNilBase(t *testing.T) {
	if got := ScriptURLs(`<script src="/a.js"></script>`, nil); got != nil {
		t.Fatalf("got %q, want nil", got)
	}
}

func TestScriptURLsLimit(t *testing.T) {
	var b strings.Builder
	for i := range 30 {
		fmt.Fprintf(&b, `<script src="/s%d.js"></script>`, i)
	}
	got := ScriptURLs(b.String(), mustURL(t, "https://example.com/"))
	if len(got) != 20 {
		t.Fatalf("len = %d, want 20", len(got))
	}
	if got[19] != "https://example.com/s19.js" {
		t.Fatalf("last = %q", got[19])
	}
}
