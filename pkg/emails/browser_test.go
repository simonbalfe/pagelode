package emails

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/simonbalfe/pagelode/pkg/browser"
	"github.com/simonbalfe/pagelode/pkg/browser/browsertest"
	"github.com/simonbalfe/pagelode/pkg/httpfetch"
	"github.com/simonbalfe/pagelode/pkg/limit"
	"github.com/simonbalfe/pagelode/pkg/memory"
)

func TestAuthenticatedEmailCrawl(t *testing.T) {
	if os.Getenv("PAGELODE_BROWSER_TESTS") != "1" {
		t.Skip("set PAGELODE_BROWSER_TESTS=1 to run browser service integration")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login":
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, `<html><body><script>document.cookie='account=test-account;path=/';localStorage.setItem('account','test-storage');</script></body></html>`)
		case "/api/contact":
			w.Header().Set("Content-Type", "application/json")
			cookie, err := r.Cookie("account")
			if err == nil && cookie.Value == "test-account" && r.Header.Get("X-Account") == "test-storage" {
				fmt.Fprint(w, `{"email":"member@example.org"}`)
			} else {
				fmt.Fprint(w, `{"authenticated":false}`)
			}
		default:
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, `<html><body><h1>Member directory</h1><a href="/contact">Contact</a><script>fetch('/api/contact',{headers:{'X-Account':localStorage.getItem('account')||''}})</script></body></html>`)
		}
	}))
	defer server.Close()
	client, err := browser.New([]string{browsertest.Start(t)}, "", 500*time.Millisecond, 4, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	headless := true
	for _, login := range []struct{ profile, startURL string }{{"member", server.URL + "/login"}, {"other", server.URL + "/"}} {
		session, err := client.CreateSession(ctx, browser.SessionRequest{Profile: login.profile, CreateProfile: true, Headless: &headless, StartURL: login.startURL})
		if err != nil {
			t.Fatal(err)
		}
		if err := client.CloseSession(ctx, session.ID); err != nil {
			t.Fatal(err)
		}
	}
	loader := NewLoader(httpfetch.New(""), client, nil, memory.NewRoutes(time.Hour, nil))
	service := New(loader, limit.New(4, 10), 4)
	for _, test := range []struct {
		name  string
		count int
	}{{"member", 1}, {"other", 0}} {
		report, err := service.Find(ctx, Request{URL: server.URL, Profile: test.name, MaxPages: 2})
		if err != nil {
			t.Fatal(err)
		}
		if len(report.Emails) != test.count || report.Summary.PagesFailed != 0 {
			t.Fatalf("profile %s: %#v", test.name, report)
		}
		data, err := json.Marshal(report)
		if err != nil {
			t.Fatal(err)
		}
		if test.count > 0 && report.Emails[0].Address != "member@example.org" {
			t.Fatalf("unexpected address: %s", data)
		}
	}
}
