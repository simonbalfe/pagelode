package httpfetch

import (
	"bytes"
	"compress/gzip"
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"

	"github.com/simonbalfe/pagelode/page"
)

func TestFetchReadsCompressedSitemapAndJavaScript(t *testing.T) {
	t.Parallel()

	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	if _, err := writer.Write([]byte(`<urlset><url><loc>https://example.test/a</loc></url></urlset>`)); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	binary := append([]byte{0x1f, 0x8b}, make([]byte, 20)...)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/sitemap.xml.gz":
			w.Header().Set("Content-Type", "application/x-gzip")
			_, _ = w.Write(compressed.Bytes())
		case "/archive.gz":
			w.Header().Set("Content-Type", "application/gzip")
			_, _ = w.Write(binary)
		case "/app.js":
			w.Header().Set("Content-Type", "application/javascript")
			_, _ = w.Write([]byte(`fetch("/api/items")`))
		}
	}))
	defer server.Close()

	tests := []struct {
		path     string
		wantType page.ContentType
		wantText string
	}{
		{"/sitemap.xml.gz", page.ContentText, `<urlset><url><loc>https://example.test/a</loc></url></urlset>`},
		{"/app.js", page.ContentText, `fetch("/api/items")`},
		{"/archive.gz", page.ContentUnknown, ""},
	}
	for _, test := range tests {
		document, err := New("").Fetch(context.Background(), server.URL+test.path, page.Session{})
		if err != nil {
			t.Fatalf("Fetch(%s) error = %v", test.path, err)
		}
		if document.Type != test.wantType || document.Text != test.wantText {
			t.Errorf("Fetch(%s) = type %q text %q, want type %q text %q", test.path, document.Type, document.Text, test.wantType, test.wantText)
		}
	}
}

func TestSubmitPostsFormAndKeepsRedirectCookies(t *testing.T) {
	t.Parallel()

	var landed atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/landing" {
			landed.Store(true)
			return
		}
		if err := request.ParseForm(); err != nil {
			t.Errorf("parse form: %v", err)
		}
		if request.Method != http.MethodPost || request.PostForm.Get("token") != "solved" || request.PostForm.Get("continue") != "/landing" {
			t.Errorf("request = %s %v", request.Method, request.PostForm)
		}
		if cookie, err := request.Cookie("session"); err != nil || cookie.Value != "browser" {
			t.Errorf("session cookie = %v, %v", cookie, err)
		}
		http.SetCookie(writer, &http.Cookie{Name: "exemption", Value: "granted", Path: "/"})
		http.Redirect(writer, request, "/landing", http.StatusFound)
	}))
	defer server.Close()

	form := Form{Method: "post", Action: server.URL + "/sorry/index", Values: url.Values{"token": {"solved"}, "continue": {"/landing"}}, Referer: server.URL + "/sorry/index"}
	session := page.Session{UserAgent: "Browser", Cookies: []page.Cookie{{Name: "session", Value: "browser"}}}
	document, err := New("").Submit(context.Background(), form, session)
	if err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	if document.StatusCode != http.StatusFound {
		t.Errorf("status = %d, want %d", document.StatusCode, http.StatusFound)
	}
	if landed.Load() {
		t.Error("Submit() followed the redirect")
	}
	if !hasCookie(document.Session.Cookies, "exemption", "granted") || !hasCookie(document.Session.Cookies, "session", "browser") {
		t.Errorf("cookies = %#v, want exemption and session", document.Session.Cookies)
	}
}

func hasCookie(cookies []page.Cookie, name string, value string) bool {
	for _, cookie := range cookies {
		if cookie.Name == name && cookie.Value == value {
			return true
		}
	}
	return false
}
