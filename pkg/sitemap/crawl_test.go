package sitemap

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/simonbalfe/pagelode/pkg/page"
)

type fixtureSite struct {
	server        *httptest.Server
	tagHits       atomic.Int32
	copyChildHits atomic.Int32
	privateHits   atomic.Int32
}

func newFixtureSite(t *testing.T) *fixtureSite {
	t.Helper()
	site := &fixtureSite{}
	long := strings.Repeat("A shared paragraph that is identical on both copies of this page. ", 8)
	site.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/robots.txt":
			fmt.Fprintf(w, "User-agent: *\nDisallow: /private/\nSitemap: %s/sitemap.xml.gz\n", site.server.URL)
		case r.URL.Path == "/sitemap.xml.gz":
			var compressed bytes.Buffer
			writer := gzip.NewWriter(&compressed)
			fmt.Fprintf(writer, `<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9"><url><loc>%[1]s/orphan</loc></url><url><loc>%[1]s/about</loc></url></urlset>`, site.server.URL)
			_ = writer.Close()
			w.Header().Set("Content-Type", "application/x-gzip")
			_, _ = w.Write(compressed.Bytes())
		case r.URL.Path == "/static/app.js":
			w.Header().Set("Content-Type", "application/javascript")
			fmt.Fprint(w, `const api = {users: "/api/users", orders: "/api/orders"}; fetch("/api/users"); const png = "/img/logo.png";`)
		case r.URL.Path == "/":
			var links strings.Builder
			for i := 1; i <= 30; i++ {
				fmt.Fprintf(&links, `<a href="/tag/t%02d">Tag %d</a>`, i, i)
			}
			fmt.Fprintf(w, `<html><body><h1>Home</h1><a href="/about">About</a><a href="/docs/">Docs</a>%s<a href="/copy-1">C1</a><a href="/copy-2">C2</a><a href="/private/area">Private</a><a href="/spa">App</a>
<script src="/static/app.js"></script><script>fetch("/api/inline")</script></body></html>`, links.String())
		case r.URL.Path == "/about":
			fmt.Fprint(w, `<html><body><h1>About</h1><a href="/about/team">Team</a></body></html>`)
		case r.URL.Path == "/docs/":
			fmt.Fprint(w, `<html><body><h1>Docs</h1><a href="/docs/intro">Intro</a><a href="/docs/setup">Setup</a></body></html>`)
		case strings.HasPrefix(r.URL.Path, "/tag/"):
			site.tagHits.Add(1)
			name := strings.TrimPrefix(r.URL.Path, "/tag/")
			fmt.Fprintf(w, `<html><body><h1>Tag %[1]s</h1><a href="/post/%[1]s">Post</a></body></html>`, name)
		case r.URL.Path == "/copy-1":
			fmt.Fprintf(w, `<html><body><p>%s</p><a href="/copy-1-child">child</a></body></html>`, long)
		case r.URL.Path == "/copy-2":
			fmt.Fprintf(w, `<html><body><p>%s</p><a href="/copy-2-child">child</a></body></html>`, long)
		case r.URL.Path == "/copy-1-child" || r.URL.Path == "/copy-2-child":
			site.copyChildHits.Add(1)
			fmt.Fprint(w, `<html><body><h1>Child</h1></body></html>`)
		case r.URL.Path == "/private/area":
			site.privateHits.Add(1)
			fmt.Fprint(w, `<html><body><a href="/private/secret">Secret</a></body></html>`)
		case r.URL.Path == "/spa":
			fmt.Fprint(w, `<html><body><div id="root"></div><noscript>You need to enable JavaScript to run this app.</noscript></body></html>`)
		default:
			fmt.Fprint(w, `<html><body><h1>Leaf</h1></body></html>`)
		}
	}))
	t.Cleanup(site.server.Close)
	return site
}

func (s *fixtureSite) paths(report Report) map[string]string {
	result := map[string]string{}
	for _, entry := range report.URLs {
		result[strings.TrimPrefix(entry.URL, s.server.URL)] = entry.Source
	}
	return result
}

func TestMapCrawlsLinksBreadthFirstToTheRequestedDepth(t *testing.T) {
	site := newFixtureSite(t)
	tests := []struct {
		depth   int
		want    []string
		missing []string
	}{
		{depth: 1, want: []string{"/", "/about", "/docs/", "/tag/t01", "/orphan"}, missing: []string{"/about/team", "/docs/intro", "/post/t01"}},
		{depth: 2, want: []string{"/about/team", "/docs/intro", "/docs/setup", "/post/t01", "/copy-1-child"}},
		{depth: 3, want: []string{"/about/team", "/docs/intro", "/post/t01"}},
	}
	for _, test := range tests {
		report, err := newService().Map(context.Background(), Request{URL: site.server.URL, Depth: test.depth})
		if err != nil {
			t.Fatal(err)
		}
		found := site.paths(report)
		for _, path := range test.want {
			if _, ok := found[path]; !ok {
				t.Errorf("depth %d: %s not found; got %v", test.depth, path, found)
			}
		}
		for _, path := range test.missing {
			if _, ok := found[path]; ok {
				t.Errorf("depth %d: %s found, want it beyond the crawl", test.depth, path)
			}
		}
	}
}

func TestMapRecordsWhichSourceFoundEachURL(t *testing.T) {
	site := newFixtureSite(t)
	report, err := newService().Map(context.Background(), Request{URL: site.server.URL})
	if err != nil {
		t.Fatal(err)
	}
	found := site.paths(report)
	for path, want := range map[string]string{"/orphan": sourceSitemap, "/": sourcePage, "/docs/": sourcePage, "/about/team": sourceLink, "/docs/intro": sourceLink} {
		if found[path] != want {
			t.Errorf("source of %s = %q, want %q", path, found[path], want)
		}
	}
}

func TestMapReadsCompressedSitemaps(t *testing.T) {
	site := newFixtureSite(t)
	report, err := newService().Map(context.Background(), Request{URL: site.server.URL, Depth: 1})
	if err != nil {
		t.Fatal(err)
	}
	if report.Summary.SitemapsRead != 1 || site.paths(report)["/orphan"] != sourceSitemap {
		t.Errorf("sitemaps read = %d, /orphan source = %q; warnings %v", report.Summary.SitemapsRead, site.paths(report)["/orphan"], report.Warnings)
	}
}

func TestMapDoesNotFetchPagesRobotsDisallows(t *testing.T) {
	site := newFixtureSite(t)
	report, err := newService().Map(context.Background(), Request{URL: site.server.URL, Depth: 3})
	if err != nil {
		t.Fatal(err)
	}
	found := site.paths(report)
	if _, ok := found["/private/area"]; !ok {
		t.Error("/private/area is linked and should still be listed")
	}
	if _, ok := found["/private/secret"]; ok {
		t.Error("/private/secret was discovered through a page robots.txt disallows")
	}
	if site.privateHits.Load() != 0 {
		t.Errorf("/private/area was fetched %d times, want 0", site.privateHits.Load())
	}
	if report.Summary.RobotsSkipped != 1 {
		t.Errorf("robotsSkipped = %d, want 1", report.Summary.RobotsSkipped)
	}
}

func TestMapExpandsOnlyASampleOfEachURLShape(t *testing.T) {
	site := newFixtureSite(t)
	report, err := newService().Map(context.Background(), Request{URL: site.server.URL})
	if err != nil {
		t.Fatal(err)
	}
	found := site.paths(report)
	if site.tagHits.Load() != shapeSampleLimit {
		t.Errorf("tag pages fetched = %d, want the sample limit %d", site.tagHits.Load(), shapeSampleLimit)
	}
	if _, ok := found["/tag/t30"]; !ok {
		t.Error("/tag/t30 is linked from the home page and must be listed even though it is not fetched")
	}
	if _, ok := found["/post/t01"]; !ok {
		t.Error("/post/t01 should be found through a sampled tag page")
	}
	if _, ok := found["/post/t30"]; ok {
		t.Error("/post/t30 was found through an unsampled tag page")
	}
}

func TestMapDoesNotExpandNearDuplicatePages(t *testing.T) {
	site := newFixtureSite(t)
	report, err := newService().Map(context.Background(), Request{URL: site.server.URL, Depth: 3})
	if err != nil {
		t.Fatal(err)
	}
	found := site.paths(report)
	if _, ok := found["/copy-1-child"]; !ok {
		t.Error("/copy-1-child is linked and must be listed")
	}
	if _, ok := found["/copy-2-child"]; !ok {
		t.Error("/copy-2-child is linked from a duplicate page and must still be listed")
	}
	if site.copyChildHits.Load() != 1 {
		t.Errorf("copy children fetched %d times, want 1: only the first copy is expanded", site.copyChildHits.Load())
	}
}

func TestMapReportsEndpointsFromScripts(t *testing.T) {
	site := newFixtureSite(t)
	report, err := newService().Map(context.Background(), Request{URL: site.server.URL})
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(report.Endpoints, " ")
	for _, want := range []string{"/api/users", "/api/orders", "/api/inline"} {
		if !strings.Contains(got, want) {
			t.Errorf("endpoints %v are missing %s", report.Endpoints, want)
		}
	}
	if strings.Contains(got, "logo.png") {
		t.Errorf("endpoints %v include a static asset", report.Endpoints)
	}
}

func TestMapGroupsURLsIntoTemplates(t *testing.T) {
	site := newFixtureSite(t)
	report, err := newService().Map(context.Background(), Request{URL: site.server.URL})
	if err != nil {
		t.Fatal(err)
	}
	for _, template := range report.Templates {
		if template.Pattern == "/tag/*" {
			if template.Count != 30 {
				t.Errorf("/tag/* count = %d, want 30", template.Count)
			}
			return
		}
	}
	t.Errorf("templates %v have no /tag/* group", report.Templates)
}

type fakeBrowser struct {
	site      *fixtureSite
	fetched   []string
	explored  []string
	unusable  bool
	exploring []string
}

func (b *fakeBrowser) Fetch(_ context.Context, target string, _ page.Session) (page.Document, error) {
	b.fetched = append(b.fetched, strings.TrimPrefix(target, b.site.server.URL))
	return page.Document{Provider: page.ProviderPatchright, StatusCode: 200, FinalURL: target, Type: page.ContentHTML,
		HTML: `<html><body><h1>Rendered</h1><a href="/spa/page1">One</a></body></html>`}, nil
}

func (b *fakeBrowser) Explore(_ context.Context, target string, _ page.Session, _ page.ExploreOptions) (page.Document, error) {
	b.explored = append(b.explored, strings.TrimPrefix(target, b.site.server.URL))
	return page.Document{Provider: page.ProviderPatchright, StatusCode: 200, FinalURL: target, Type: page.ContentHTML,
		HTML:         `<html><body><h1>Home</h1><a href="/revealed">Revealed</a></body></html>`,
		ExploredURLs: []string{b.site.server.URL + "/tab-two"}}, nil
}

func TestMapRendersOnlyPagesThatNeedJavaScript(t *testing.T) {
	site := newFixtureSite(t)
	browser := &fakeBrowser{site: site}
	report, err := newService().WithBrowser(browser).Map(context.Background(), Request{URL: site.server.URL, Render: renderAuto, Depth: 3})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(browser.fetched, []string{"/spa"}) {
		t.Errorf("browser fetched %v, want only /spa", browser.fetched)
	}
	if got := site.paths(report)["/spa/page1"]; got != sourceRendered {
		t.Errorf("source of /spa/page1 = %q, want %q", got, sourceRendered)
	}
	if report.Summary.PagesRendered != 1 {
		t.Errorf("pagesRendered = %d, want 1", report.Summary.PagesRendered)
	}
}

func TestMapExploresTheStartPage(t *testing.T) {
	site := newFixtureSite(t)
	browser := &fakeBrowser{site: site}
	report, err := newService().WithBrowser(browser).Map(context.Background(), Request{URL: site.server.URL, Render: renderAuto, Explore: true, Depth: 1})
	if err != nil {
		t.Fatal(err)
	}
	found := site.paths(report)
	if !slices.Equal(browser.explored, []string{"/"}) {
		t.Errorf("explored %v, want only the start page", browser.explored)
	}
	if found["/tab-two"] != sourceExplore || found["/revealed"] != sourceRendered {
		t.Errorf("/tab-two source = %q, /revealed source = %q", found["/tab-two"], found["/revealed"])
	}
}

func TestMapWarnsWhenRenderingIsRequestedWithoutABrowser(t *testing.T) {
	site := newFixtureSite(t)
	report, err := newService().Map(context.Background(), Request{URL: site.server.URL, Render: renderAuto, Depth: 1})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(report.Warnings, func(w string) bool { return strings.Contains(w, "browser rendering is unavailable") }) {
		t.Errorf("warnings %v do not mention the missing browser", report.Warnings)
	}
}

func TestRequestValidateCrawlOptions(t *testing.T) {
	tests := []struct {
		name    string
		request Request
		wantErr bool
	}{
		{"defaults", Request{URL: "example.com"}, false},
		{"depth too large", Request{URL: "example.com", Depth: 6}, true},
		{"negative depth", Request{URL: "example.com", Depth: -1}, true},
		{"unknown render mode", Request{URL: "example.com", Render: "sometimes"}, true},
		{"explore without rendering", Request{URL: "example.com", Explore: true}, true},
		{"explore with auto rendering", Request{URL: "example.com", Render: renderAuto, Explore: true}, false},
	}
	for _, test := range tests {
		_, err := test.request.Validate()
		if (err != nil) != test.wantErr {
			t.Errorf("%s: error = %v, wantErr %v", test.name, err, test.wantErr)
		}
	}
	validated, _ := Request{URL: "example.com"}.Validate()
	if validated.Depth != defaultDepth || validated.Render != renderNever {
		t.Errorf("defaults = depth %d render %q, want %d and %q", validated.Depth, validated.Render, defaultDepth, renderNever)
	}
}

func TestRobotsRulesPickTheLongestMatch(t *testing.T) {
	rules := parseRobotsRules(`User-agent: googlebot
Disallow: /everything

User-agent: *
Disallow: /private/
Allow: /private/public/
Disallow: /*.pdf$
Disallow: /search?q=
Sitemap: https://example.test/sitemap.xml
`)
	tests := map[string]bool{
		"https://example.test/":                      false,
		"https://example.test/everything":            false,
		"https://example.test/private/a":             true,
		"https://example.test/private/public/a":      false,
		"https://example.test/docs/file.pdf":         true,
		"https://example.test/docs/file.pdf?x=1":     false,
		"https://example.test/search?q=shoes":        true,
		"https://example.test/search":                false,
		"https://example.test/blog/private/not-this": false,
	}
	for rawURL, want := range tests {
		if got := rules.blocks(rawURL); got != want {
			t.Errorf("blocks(%s) = %v, want %v", rawURL, got, want)
		}
	}
	if len(parseRobotsRules("")) != 0 || parseRobotsRules("User-agent: *\nDisallow:\n").blocks("https://example.test/a") {
		t.Error("an empty Disallow must block nothing")
	}
}
