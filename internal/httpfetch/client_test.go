package httpfetch

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"

	"github.com/simonbalfe/pagelode/internal/page"
)

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
