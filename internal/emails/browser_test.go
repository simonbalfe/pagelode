package emails

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/simonbalfe/pagelode/internal/httpfetch"
	"github.com/simonbalfe/pagelode/internal/limit"
	"github.com/simonbalfe/pagelode/internal/memory"
	"github.com/simonbalfe/pagelode/internal/page"
	"github.com/simonbalfe/pagelode/internal/patchright"
	"github.com/simonbalfe/pagelode/internal/profile"
)

func TestAuthenticatedEmailCrawl(t *testing.T) {
	if os.Getenv("PAGELODE_BROWSER_TESTS") != "1" {
		t.Skip("set PAGELODE_BROWSER_TESTS=1 to run Patchright integration")
	}
	t.Setenv("PAGELODE_PATCHRIGHT_HEADLESS", "true")
	t.Setenv("PAGELODE_PATCHRIGHT_PROFILE", t.TempDir())
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
	worker, err := filepath.Abs("../../browser/src/worker.ts")
	if err != nil {
		t.Fatal(err)
	}
	client, err := patchright.New("bun", []string{worker}, "")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := client.Close(); err != nil {
			t.Error(err)
		}
	}()
	root := t.TempDir()
	directory, err := profile.Directory(root, "member", true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := profile.Directory(root, "other", true); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	if _, err := client.Capture(ctx, server.URL+"/login", page.Session{}, page.CaptureOptions{WaitMS: 100, ProfileDirectory: directory}); err != nil {
		t.Fatal(err)
	}
	loader := NewLoader(httpfetch.New(""), nil, client, nil, memory.NewRoutes(time.Hour, nil), limit.New(1, 10), false)
	service := New(loader, limit.New(4, 10), 4, root)
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
