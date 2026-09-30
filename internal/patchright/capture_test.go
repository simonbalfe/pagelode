package patchright_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/simonbalfe/pagelode/internal/discovery"
	"github.com/simonbalfe/pagelode/internal/page"
	"github.com/simonbalfe/pagelode/internal/patchright"
)

func TestWorkerCapture(t *testing.T) {
	if os.Getenv("PAGELODE_BROWSER_TESTS") != "1" {
		t.Skip("set PAGELODE_BROWSER_TESTS=1 to run Patchright integration")
	}
	t.Setenv("PAGELODE_PATCHRIGHT_HEADLESS", "true")
	t.Setenv("PAGELODE_PATCHRIGHT_PROFILE", t.TempDir())
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		if _, err := w.Write([]byte(`<html><head><title>Capture fixture</title></head><body><h1>Listings</h1><script>fetch('/api/listings?page=1',{headers:{Authorization:'Bearer private-key'}});</script></body></html>`)); err != nil {
			t.Error(err)
		}
	})
	mux.HandleFunc("/api/listings", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if _, err := w.Write([]byte(`{"items":[{"id":1}],"nextCursor":"private-cursor"}`)); err != nil {
			t.Error(err)
		}
	})
	server := httptest.NewServer(mux)
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
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	document, err := client.Capture(ctx, server.URL, page.Session{}, page.CaptureOptions{WaitMS: 200})
	if err != nil {
		t.Fatal(err)
	}
	report := discovery.Analyze(server.URL, document)
	if report.Summary.DataRequests != 1 {
		t.Fatalf("report = %+v, want 1 data request", report)
	}
	found := false
	for _, endpoint := range report.Endpoints {
		if endpoint.Path == "/api/listings" {
			found = true
			if len(endpoint.ResponseSchema) == 0 || len(endpoint.Auth) == 0 {
				t.Errorf("endpoint = %+v, want response schema and auth", endpoint)
			}
		}
	}
	if !found {
		t.Errorf("endpoints = %+v, want listings", report.Endpoints)
	}
}

func TestAuthenticatedProfilePersistsAndIsolatesStorage(t *testing.T) {
	if os.Getenv("PAGELODE_BROWSER_TESTS") != "1" {
		t.Skip("set PAGELODE_BROWSER_TESTS=1 to run Patchright integration")
	}
	t.Setenv("PAGELODE_PATCHRIGHT_HEADLESS", "true")
	t.Setenv("PAGELODE_PATCHRIGHT_PROFILE", t.TempDir())
	mux := http.NewServeMux()
	mux.HandleFunc("/login", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, err := w.Write([]byte(`<html><body><script>document.cookie='session=private-profile-cookie;path=/';localStorage.setItem('session','private-profile-storage');</script></body></html>`))
		if err != nil {
			t.Error(err)
		}
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, err := w.Write([]byte(`<html><body><script>fetch('/api/account',{headers:{'X-Session':localStorage.getItem('session')||''}});</script></body></html>`))
		if err != nil {
			t.Error(err)
		}
	})
	mux.HandleFunc("/api/account", func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie("session")
		authenticated := err == nil && cookie.Value == "private-profile-cookie" && r.Header.Get("X-Session") == "private-profile-storage"
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]bool{"authenticated": authenticated}); err != nil {
			t.Error(err)
		}
	})
	server := httptest.NewServer(mux)
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
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	directory := t.TempDir()
	login := exec.CommandContext(ctx, "bun", worker, "--login", server.URL+"/login")
	login.Env = append(os.Environ(), "PAGELODE_PATCHRIGHT_PROFILE="+directory, "PAGELODE_PROFILE_SESSION=true")
	login.Stdin = strings.NewReader("\n")
	if output, err := login.CombinedOutput(); err != nil {
		t.Fatalf("login worker: %v: %s", err, output)
	}
	for _, test := range []struct {
		directory     string
		authenticated bool
	}{{directory, true}, {t.TempDir(), false}} {
		document, err := client.Capture(ctx, server.URL, page.Session{}, page.CaptureOptions{WaitMS: 200, ProfileDirectory: test.directory})
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, entry := range document.Traffic.Entries {
			if !strings.HasSuffix(entry.URL, "/api/account") {
				continue
			}
			found = true
			var payload struct {
				Authenticated bool `json:"authenticated"`
			}
			if err := json.Unmarshal([]byte(entry.ResponseBody), &payload); err != nil {
				t.Fatal(err)
			}
			if payload.Authenticated != test.authenticated {
				t.Errorf("authenticated=%t,want %t", payload.Authenticated, test.authenticated)
			}
		}
		if !found {
			t.Fatal("account request missing")
		}
		report, err := json.Marshal(discovery.Analyze(server.URL, document))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(report), "private-profile") {
			t.Error("report exposes profile credentials")
		}
	}
}
