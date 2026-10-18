package sitemap

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/simonbalfe/pagelode/internal/httpfetch"
	"github.com/simonbalfe/pagelode/internal/limit"
)

func newService() *Service {
	return New(httpfetch.New(""), limit.New(2, 10))
}

func urls(report Report) []string {
	result := []string{}
	for _, entry := range report.URLs {
		result = append(result, strings.TrimPrefix(entry.URL, "http://"))
	}
	return result
}

func TestMapFollowsRobotsIndexesAndPageLinks(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/robots.txt":
			fmt.Fprintf(w, "User-agent: *\n# comment\nSitemap: %s/index.xml\n", server.URL)
		case "/index.xml":
			w.Header().Set("Content-Type", "application/xml")
			fmt.Fprintf(w, `<sitemapindex xmlns="http://www.sitemaps.org/schemas/sitemap/0.9"><sitemap><loc>%s/posts.xml</loc></sitemap><sitemap><loc>https://other.test/x.xml</loc></sitemap></sitemapindex>`, server.URL)
		case "/posts.xml":
			w.Header().Set("Content-Type", "text/xml")
			fmt.Fprintf(w, `<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
<url><loc>%[1]s/blog/a?utm_source=x</loc><lastmod>2026-01-02</lastmod></url>
<url><loc>%[1]s/blog/a</loc></url>
<url><loc>%[1]s/admin/secret</loc></url>
<url><loc>https://other.test/blog/b</loc></url>
<url><loc>%[1]s/logo.png</loc></url>
</urlset>`, server.URL)
		case "/":
			fmt.Fprint(w, `<a href="/about#team">About</a><a href="/blog/a">Dup</a><a href="/admin/x">Admin</a><a href="https://other.test/">Out</a>`)
		default:
			w.WriteHeader(404)
		}
	}))
	defer server.Close()

	report, err := newService().Map(context.Background(), Request{URL: server.URL, Exclude: []string{"/admin/*"}})
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(urls(report), " ")
	host := strings.TrimPrefix(server.URL, "http://")
	want := fmt.Sprintf("%[1]s/blog/a %[1]s/ %[1]s/about", host)
	if got != want {
		t.Fatalf("urls:\n got %s\nwant %s", got, want)
	}
	first := report.URLs[0]
	if first.Source != "sitemap" || first.LastModified != "2026-01-02" {
		t.Errorf("first entry: %#v", first)
	}
	if report.URLs[2].Source != "page" || report.Summary.SitemapsRead != 2 || report.Outcome != "ok" || report.Summary.Limited {
		t.Errorf("report: %#v", report)
	}
}

func TestMapFallsBackToPageLinksAndHonorsInclude(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			fmt.Fprint(w, `<a href="/docs/one">1</a><a href="/docs/two">2</a><a href="/pricing">P</a>`)
			return
		}
		w.WriteHeader(404)
	}))
	defer server.Close()

	report, err := newService().Map(context.Background(), Request{URL: server.URL, Include: []string{"/docs/*"}})
	if err != nil {
		t.Fatal(err)
	}
	host := strings.TrimPrefix(server.URL, "http://")
	if got, want := strings.Join(urls(report), " "), host+"/docs/one "+host+"/docs/two"; got != want {
		t.Fatalf("urls: got %s want %s", got, want)
	}
}

func TestMapLimitMarksReportLimited(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			fmt.Fprint(w, `<a href="/a">a</a><a href="/b">b</a><a href="/c">c</a>`)
			return
		}
		w.WriteHeader(404)
	}))
	defer server.Close()

	report, err := newService().Map(context.Background(), Request{URL: server.URL, Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.URLs) != 2 || !report.Summary.Limited {
		t.Fatalf("report: %#v", report)
	}
}

func TestMapSubdomainsAndUnreachableSite(t *testing.T) {
	scope := newScope(mustParse(t, "https://www.example.com"), false)
	if scope.normalize("https://docs.example.com/a", "https://example.com") != "" {
		t.Error("subdomain accepted without subdomains option")
	}
	if scope.normalize("https://example.com/a", "https://example.com") == "" {
		t.Error("www-equivalent host rejected")
	}
	wide := newScope(mustParse(t, "https://example.com"), true)
	if wide.normalize("https://docs.example.com/a", "https://example.com") == "" || wide.normalize("https://notexample.com/a", "https://example.com") != "" {
		t.Error("subdomain scope wrong")
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) }))
	defer server.Close()
	report, err := newService().Map(context.Background(), Request{URL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	if report.Outcome != "failed" {
		t.Fatalf("outcome: %s", report.Outcome)
	}
}

func TestValidateRejectsBadInput(t *testing.T) {
	for _, request := range []Request{
		{URL: "ftp://example.com"},
		{URL: "example.com", Limit: 5001},
		{URL: "example.com", MaxDurationMS: 10},
		{URL: "example.com", Include: []string{"blog/*"}},
	} {
		if _, err := request.Validate(); err == nil {
			t.Errorf("accepted %#v", request)
		}
	}
}

func mustParse(t *testing.T, raw string) *url.URL {
	t.Helper()
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}
