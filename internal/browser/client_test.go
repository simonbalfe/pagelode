package browser

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/simonbalfe/pagelode/internal/limit"
	"github.com/simonbalfe/pagelode/internal/page"
)

type recorded struct {
	method        string
	path          string
	authorization string
	body          map[string]any
}

func fakeService(t *testing.T, status int, response string) (*httptest.Server, chan recorded) {
	t.Helper()
	requests := make(chan recorded, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var record recorded
		record.method = r.Method
		record.path = r.URL.Path
		record.authorization = r.Header.Get("Authorization")
		data, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		if len(data) > 0 {
			if err := json.Unmarshal(data, &record.body); err != nil {
				t.Errorf("request body %s: %v", data, err)
			}
		}
		requests <- record
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if _, err := io.WriteString(w, response); err != nil {
			t.Error(err)
		}
	}))
	t.Cleanup(server.Close)
	return server, requests
}

func newClient(t *testing.T, baseURL, token, proxyURL string) *Client {
	t.Helper()
	client, err := New(baseURL, token, proxyURL)
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func TestNewRejectsInvalidURL(t *testing.T) {
	for _, baseURL := range []string{"", "127.0.0.1:8090", "ftp://host", "http://"} {
		if _, err := New(baseURL, "", ""); err == nil {
			t.Errorf("New(%q) error = nil, want error", baseURL)
		}
	}
}

func TestRenderRequestBody(t *testing.T) {
	cookies := []page.Cookie{{Name: "sid", Value: "1", Domain: "example.org", Path: "/"}}
	for _, test := range []struct {
		name        string
		capture     bool
		profile     string
		wantCapture bool
	}{
		{name: "fetch"},
		{name: "capture", capture: true, wantCapture: true},
		{name: "capture with profile", capture: true, profile: "member", wantCapture: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			server, requests := fakeService(t, http.StatusOK, `{"status_code":200}`)
			client := newClient(t, server.URL, "", "http://proxy:8080")
			ctx := context.Background()
			session := page.Session{Cookies: cookies}
			var err error
			if test.capture {
				_, err = client.Capture(ctx, "https://example.org/", session, page.CaptureOptions{WaitMS: 250, Profile: test.profile})
			} else {
				_, err = client.Fetch(ctx, "https://example.org/", session)
			}
			if err != nil {
				t.Fatal(err)
			}
			record := <-requests
			if record.method != http.MethodPost || record.path != "/v1/render" {
				t.Errorf("request = %s %s, want POST /v1/render", record.method, record.path)
			}
			body := record.body
			if body["url"] != "https://example.org/" || body["wait_until"] != "domcontentloaded" || body["settle_challenge"] != true || body["proxy_url"] != "http://proxy:8080" {
				t.Errorf("body = %v", body)
			}
			if body["timeout_ms"] != float64(45000) {
				t.Errorf("timeout_ms = %v, want 45000", body["timeout_ms"])
			}
			if got, ok := body["cookies"].([]any); !ok || len(got) != 1 || got[0].(map[string]any)["name"] != "sid" {
				t.Errorf("cookies = %v, want sid cookie", body["cookies"])
			}
			capture, hasCapture := body["capture"].(map[string]any)
			if hasCapture != test.wantCapture {
				t.Errorf("capture present = %t, want %t", hasCapture, test.wantCapture)
			}
			if hasCapture && capture["wait_ms"] != float64(250) {
				t.Errorf("capture = %v, want wait_ms 250", capture)
			}
			profile, hasProfile := body["profile"]
			if hasProfile != (test.profile != "") || (hasProfile && profile != test.profile) {
				t.Errorf("profile = %v (present %t), want %q", profile, hasProfile, test.profile)
			}
		})
	}
}

func TestRenderTimeoutFollowsDeadline(t *testing.T) {
	server, requests := fakeService(t, http.StatusOK, `{"status_code":200}`)
	client := newClient(t, server.URL, "", "")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := client.Fetch(ctx, "https://example.org/", page.Session{}); err != nil {
		t.Fatal(err)
	}
	record := <-requests
	got, _ := record.body["timeout_ms"].(float64)
	if got > 10000 || got < 9000 {
		t.Errorf("timeout_ms = %v, want about 10000", got)
	}

	short, cancelShort := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancelShort()
	if _, err := client.Fetch(short, "https://example.org/", page.Session{}); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Fetch() error = %v, want context.DeadlineExceeded", err)
	}
	if len(requests) != 0 {
		t.Errorf("requests sent = %d, want none", len(requests))
	}
}

func TestRenderDecodesDocument(t *testing.T) {
	server, _ := fakeService(t, http.StatusOK, `{"status_code":201,"final_url":"https://example.org/final","title":"Example","html":"<p>hi</p>","user_agent":"Agent/2","cookies":[{"name":"new","value":"2"}],"traffic":{"entries":[{"id":"1","url":"https://example.org/api"}],"dropped":3,"duration_ms":40}}`)
	client := newClient(t, server.URL, "", "")
	document, err := client.Capture(context.Background(), "https://example.org/", page.Session{UserAgent: "Agent/1", Cookies: []page.Cookie{{Name: "old"}}}, page.CaptureOptions{WaitMS: 100})
	if err != nil {
		t.Fatal(err)
	}
	if document.Provider != page.ProviderPatchright || document.Type != page.ContentHTML || document.StatusCode != 201 || document.FinalURL != "https://example.org/final" || document.Title != "Example" || document.HTML != "<p>hi</p>" {
		t.Errorf("document = %+v", document)
	}
	if document.Session.UserAgent != "Agent/2" || len(document.Session.Cookies) != 1 || document.Session.Cookies[0].Name != "new" {
		t.Errorf("session = %+v, want Agent/2 with cookie new", document.Session)
	}
	if document.Traffic == nil || len(document.Traffic.Entries) != 1 || document.Traffic.Dropped != 3 || document.Traffic.DurationMS != 40 {
		t.Errorf("traffic = %+v", document.Traffic)
	}
}

func TestRenderKeepsUserAgentWhenOmitted(t *testing.T) {
	server, _ := fakeService(t, http.StatusOK, `{"status_code":200}`)
	client := newClient(t, server.URL, "", "")
	document, err := client.Fetch(context.Background(), "https://example.org/", page.Session{UserAgent: "Agent/1"})
	if err != nil {
		t.Fatal(err)
	}
	if document.Session.UserAgent != "Agent/1" {
		t.Errorf("UserAgent = %q, want Agent/1", document.Session.UserAgent)
	}
}

func TestBearerToken(t *testing.T) {
	for _, test := range []struct {
		token string
		want  string
	}{{"", ""}, {"secret", "Bearer secret"}} {
		server, requests := fakeService(t, http.StatusOK, `{}`)
		client := newClient(t, server.URL, test.token, "")
		if _, err := client.Fetch(context.Background(), "https://example.org/", page.Session{}); err != nil {
			t.Fatal(err)
		}
		record := <-requests
		if record.authorization != test.want {
			t.Errorf("token %q: Authorization = %q, want %q", test.token, record.authorization, test.want)
		}
	}
}

func TestServiceErrors(t *testing.T) {
	for _, test := range []struct {
		name      string
		status    int
		body      string
		want      string
		saturated bool
	}{
		{name: "render failed", status: http.StatusBadGateway, body: `{"error":{"code":"render_failed","message":"net::ERR_NAME_NOT_RESOLVED"}}`, want: "patchright: render_failed: net::ERR_NAME_NOT_RESOLVED"},
		{name: "profile missing", status: http.StatusNotFound, body: `{"error":{"code":"profile_not_found","message":"profile member does not exist"}}`, want: "patchright: profile_not_found: profile member does not exist"},
		{name: "saturated", status: http.StatusTooManyRequests, body: `{"error":{"code":"saturated","message":"queue full"}}`, want: "patchright: saturated: queue full", saturated: true},
		{name: "undecodable", status: http.StatusInternalServerError, body: `oops`, want: "patchright: http_500: Internal Server Error"},
	} {
		t.Run(test.name, func(t *testing.T) {
			server, _ := fakeService(t, test.status, test.body)
			client := newClient(t, server.URL, "", "")
			_, err := client.Fetch(context.Background(), "https://example.org/", page.Session{})
			if err == nil || !strings.HasPrefix(err.Error(), test.want) {
				t.Fatalf("Fetch() error = %v, want prefix %q", err, test.want)
			}
			if got := errors.Is(err, limit.ErrSaturated); got != test.saturated {
				t.Errorf("errors.Is(err, limit.ErrSaturated) = %t, want %t", got, test.saturated)
			}
		})
	}
}

func TestCreateSession(t *testing.T) {
	server, requests := fakeService(t, http.StatusCreated, `{"id":"s1","cdp_url":"ws://host/v1/sessions/s1/cdp","profile":"member","headless":true,"expires_at":"2026-10-18T12:00:00Z"}`)
	client := newClient(t, server.URL, "secret", "http://proxy:8080")
	headless := true
	session, err := client.CreateSession(context.Background(), SessionRequest{Profile: "member", CreateProfile: true, Headless: &headless, StartURL: "https://example.org/login", TTL: 10 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	record := <-requests
	if record.method != http.MethodPost || record.path != "/v1/sessions" || record.authorization != "Bearer secret" {
		t.Errorf("request = %s %s auth %q", record.method, record.path, record.authorization)
	}
	want := map[string]any{"proxy_url": "http://proxy:8080", "profile": "member", "create_profile": true, "headless": true, "start_url": "https://example.org/login", "ttl_seconds": float64(600)}
	for key, value := range want {
		if record.body[key] != value {
			t.Errorf("body[%s] = %v, want %v", key, record.body[key], value)
		}
	}
	if len(record.body) != len(want) {
		t.Errorf("body = %v, want only %v", record.body, want)
	}
	expires := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	if session.ID != "s1" || session.CDPURL != "ws://host/v1/sessions/s1/cdp" || session.Profile != "member" || !session.Headless || !session.ExpiresAt.Equal(expires) {
		t.Errorf("session = %+v", session)
	}
}

func TestCreateSessionOmitsDefaults(t *testing.T) {
	server, requests := fakeService(t, http.StatusCreated, `{"id":"s1"}`)
	client := newClient(t, server.URL, "", "")
	if _, err := client.CreateSession(context.Background(), SessionRequest{}); err != nil {
		t.Fatal(err)
	}
	record := <-requests
	if len(record.body) != 0 {
		t.Errorf("body = %v, want {}", record.body)
	}
}

func TestCloseSession(t *testing.T) {
	server, requests := fakeService(t, http.StatusNoContent, "")
	client := newClient(t, server.URL, "", "")
	if err := client.CloseSession(context.Background(), "s1"); err != nil {
		t.Fatal(err)
	}
	record := <-requests
	if record.method != http.MethodDelete || record.path != "/v1/sessions/s1" {
		t.Errorf("request = %s %s, want DELETE /v1/sessions/s1", record.method, record.path)
	}
}

func TestCloseSessionNotFound(t *testing.T) {
	server, _ := fakeService(t, http.StatusNotFound, `{"error":{"code":"not_found","message":"unknown session"}}`)
	client := newClient(t, server.URL, "", "")
	err := client.CloseSession(context.Background(), "missing")
	if err == nil || err.Error() != "patchright: not_found: unknown session" {
		t.Errorf("CloseSession() error = %v, want not_found", err)
	}
}
