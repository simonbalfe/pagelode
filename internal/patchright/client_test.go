package patchright

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/simonbalfe/pagelode/internal/page"
)

func TestClientFetch(t *testing.T) {
	t.Setenv("PAGELODE_PATCHRIGHT_TEST_WORKER", "1")
	client, err := New(os.Args[0], []string{"-test.run=TestWorkerProcess"}, "http://proxy.example:8080")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})

	document, err := client.Fetch(context.Background(), "https://example.com", page.Session{
		UserAgent: "incoming-agent",
		Cookies:   []page.Cookie{{Name: "incoming", Value: "one", Domain: "example.com", Path: "/"}},
	})
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if document.Provider != page.ProviderPatchright {
		t.Errorf("Fetch().Provider = %q, want patchright", document.Provider)
	}
	if document.StatusCode != 200 {
		t.Errorf("Fetch().StatusCode = %d, want 200", document.StatusCode)
	}
	if document.Session.UserAgent != "worker-agent" {
		t.Errorf("Fetch().Session.UserAgent = %q, want worker-agent", document.Session.UserAgent)
	}
	if len(document.Session.Cookies) != 1 || document.Session.Cookies[0].Name != "clearance" {
		t.Errorf("Fetch().Session.Cookies = %#v, want clearance cookie", document.Session.Cookies)
	}
}

func TestClientCorrelatesConcurrentResponses(t *testing.T) {
	t.Setenv("PAGELODE_PATCHRIGHT_TEST_WORKER", "1")
	client, err := New(os.Args[0], []string{"-test.run=TestWorkerProcess"}, "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})

	urls := []string{"https://one.example", "https://two.example", "https://three.example"}
	var wait sync.WaitGroup
	errors := make(chan error, len(urls))
	for _, targetURL := range urls {
		wait.Add(1)
		go func() {
			defer wait.Done()
			document, err := client.Fetch(context.Background(), targetURL, page.Session{})
			if err != nil {
				errors <- err
				return
			}
			if document.FinalURL != targetURL {
				errors <- fmt.Errorf("final URL = %q, want %q", document.FinalURL, targetURL)
			}
		}()
	}
	wait.Wait()
	close(errors)
	for err := range errors {
		t.Errorf("concurrent Fetch() error = %v", err)
	}
}

func TestClientReturnsWorkerError(t *testing.T) {
	t.Setenv("PAGELODE_PATCHRIGHT_TEST_WORKER", "1")
	client, err := New(os.Args[0], []string{"-test.run=TestWorkerProcess"}, "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})

	_, err = client.Fetch(context.Background(), "https://error.example", page.Session{})
	if err == nil || !strings.Contains(err.Error(), "render_failed: refused") {
		t.Fatalf("Fetch() error = %v, want render_failed", err)
	}
}

func TestWorkerProcess(t *testing.T) {
	if os.Getenv("PAGELODE_PATCHRIGHT_TEST_WORKER") != "1" {
		return
	}
	decoder := json.NewDecoder(os.Stdin)
	encoder := json.NewEncoder(os.Stdout)
	for {
		var request renderRequest
		if err := decoder.Decode(&request); err != nil {
			os.Exit(0)
		}
		if strings.Contains(request.URL, "error.example") {
			if err := encoder.Encode(renderEnvelope{ID: request.ID, Error: &renderError{Code: "render_failed", Message: "refused"}}); err != nil {
				os.Exit(1)
			}
			continue
		}
		if err := encoder.Encode(renderEnvelope{
			ID: request.ID,
			OK: true,
			Data: &renderData{
				StatusCode: 200,
				FinalURL:   request.URL,
				Title:      "Example",
				HTML:       "<html><body>Example</body></html>",
				UserAgent:  "worker-agent",
				Cookies:    []page.Cookie{{Name: "clearance", Value: "two", Domain: "example.com", Path: "/"}},
			},
		}); err != nil {
			os.Exit(1)
		}
	}
}
