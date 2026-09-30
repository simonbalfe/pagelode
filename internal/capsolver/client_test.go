package capsolver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/simonbalfe/pagelode/internal/page"
)

type sessionFetcher struct {
	session page.Session
	calls   int
}

func (s *sessionFetcher) Fetch(_ context.Context, target string, session page.Session) (page.Document, error) {
	s.calls++
	s.session = session
	return page.Document{
		Provider:   page.ProviderTLS,
		StatusCode: http.StatusOK,
		FinalURL:   target,
		Type:       page.ContentHTML,
		HTML:       "<html><body>solved</body></html>",
		Session: page.Session{
			UserAgent: "Bootstrap Browser",
			Cookies:   []page.Cookie{{Name: "bootstrap", Value: "cookie"}},
		},
	}, nil
}

func TestFetchSolvesAndPassesSession(t *testing.T) {
	t.Parallel()

	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests++
		var payload map[string]any
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Errorf("decode request: %v", err)
		}
		switch request.URL.Path {
		case "/createTask":
			taskPayload := payload["task"].(map[string]any)
			if taskPayload["proxy"] != "http:proxy.example:8080:user:pass" {
				t.Errorf("proxy = %q", taskPayload["proxy"])
			}
			if taskPayload["userAgent"] != "Bootstrap Browser" || taskPayload["html"] == "" {
				t.Errorf("task = %#v", taskPayload)
			}
			_, _ = writer.Write([]byte(`{"errorId":0,"taskId":"task-1"}`))
		case "/getTaskResult":
			_, _ = writer.Write([]byte(`{"errorId":0,"status":"ready","solution":{"userAgent":"Solved Browser","cookies":{"cf_clearance":"clear"}}}`))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	downstream := &sessionFetcher{}
	client, err := New("key", server.URL, "http://user:pass@proxy.example:8080", downstream, downstream)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	client.pollWait = time.Millisecond
	document, err := client.Fetch(context.Background(), "https://example.com/page", page.Session{})
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if requests != 2 {
		t.Errorf("requests = %d, want 2", requests)
	}
	if document.Provider != page.ProviderCapSolver {
		t.Errorf("provider = %q, want capsolver", document.Provider)
	}
	if downstream.calls != 2 {
		t.Errorf("downstream calls = %d, want 2", downstream.calls)
	}
	if downstream.session.UserAgent != "Solved Browser" {
		t.Errorf("user agent = %q", downstream.session.UserAgent)
	}
	if len(downstream.session.Cookies) != 2 || downstream.session.Cookies[1].Name != "cf_clearance" {
		t.Errorf("cookies = %#v", downstream.session.Cookies)
	}
}

func TestNewIsDisabledWithoutCredentials(t *testing.T) {
	t.Parallel()

	client, err := New("", "https://api.capsolver.com", "", &sessionFetcher{}, &sessionFetcher{})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if client != nil {
		t.Errorf("New() = %#v, want nil", client)
	}
}

func TestSolverProxyRequiresAuthentication(t *testing.T) {
	t.Parallel()

	if _, err := solverProxy("http://proxy.example:8080"); err == nil {
		t.Fatal("solverProxy() error = nil")
	}
}
