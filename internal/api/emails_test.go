package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/simonbalfe/pagelode/internal/emails"
	"github.com/simonbalfe/pagelode/internal/limit"
	"github.com/simonbalfe/pagelode/internal/memory"
	"github.com/simonbalfe/pagelode/internal/page"
)

type emailLoader struct{}

func (emailLoader) Load(_ context.Context, target string, _ emails.Options) (page.Document, error) {
	return page.Document{FinalURL: target, HTML: `<footer>hello@example.org</footer>`, StatusCode: 200}, nil
}

func TestEmailsEndpoint(t *testing.T) {
	limiter := limit.New(1, 0)
	server := New(nil, nil, limiter, limit.New(1, 0), memory.NewRoutes(time.Hour, nil), time.Second, slog.New(slog.NewTextHandler(io.Discard, nil))).WithEmails(emails.New(emailLoader{}, limiter, 1, t.TempDir()))
	for _, test := range []struct {
		body   string
		status int
	}{
		{`{"url":"example.org","maxPages":1}`, 200},
		{`{"url":"example.org","maxPages":101}`, 400},
		{`{"url":"example.org","render":"invalid"}`, 400},
		{`{"url":"example.org","profile":"../escape"}`, 400},
		{`{"url":"example.org","profile":"account","render":"never"}`, 400},
		{`{"url":"example.org","unknown":true}`, 400},
		{`{"url":"example.org"} {}`, 400},
		{`{"url":"file:///etc/passwd"}`, 400},
	} {
		t.Run(test.body, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/emails", strings.NewReader(test.body))
			response := httptest.NewRecorder()
			server.Handler().ServeHTTP(response, request)
			if response.Code != test.status {
				t.Fatalf("status %d: %s", response.Code, response.Body.String())
			}
			if test.status == 200 {
				var report emails.Report
				if err := json.Unmarshal(response.Body.Bytes(), &report); err != nil {
					t.Fatal(err)
				}
				if len(report.Emails) != 1 || report.Emails[0].Address != "hello@example.org" {
					t.Fatalf("report %#v", report)
				}
			}
		})
	}
}
