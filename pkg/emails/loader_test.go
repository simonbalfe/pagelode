package emails

import (
	"context"
	"github.com/simonbalfe/pagelode/pkg/fetch"
	"testing"
	"time"
)

type fetchFunc func(context.Context, string, fetch.Session) (fetch.Document, error)

func (f fetchFunc) Fetch(ctx context.Context, target string, session fetch.Session) (fetch.Document, error) {
	return f(ctx, target, session)
}

type captureFunc func(context.Context, string, fetch.Session, fetch.CaptureOptions) (fetch.Document, error)

func (f captureFunc) Capture(ctx context.Context, target string, session fetch.Session, options fetch.CaptureOptions) (fetch.Document, error) {
	return f(ctx, target, session, options)
}

func TestLoaderRouting(t *testing.T) {
	for _, test := range []struct {
		name, body, profile string
		status              int
		protected           bool
		want                string
	}{
		{name: "HTTP footer", body: `<footer>fast@example.org</footer>`, status: 200, want: "http"},
		{name: "JavaScript shell", body: `<div id="app"></div><script src="/app.js"></script>`, status: 200, want: "browser"},
		{name: "blocked", body: `<p>Access denied</p>`, status: 403, want: "browser"},
		{name: "protected", protected: true, want: "browser"},
		{name: "profile", profile: "account", want: "browser"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var calls []string
			http := fetchFunc(func(context.Context, string, fetch.Session) (fetch.Document, error) {
				calls = append(calls, "http")
				return fetch.Document{HTML: test.body, Type: fetch.ContentHTML, FinalURL: "https://example.org/", StatusCode: test.status}, nil
			})
			browser := captureFunc(func(_ context.Context, target string, _ fetch.Session, options fetch.CaptureOptions) (fetch.Document, error) {
				calls = append(calls, "browser")
				if options.Profile != test.profile {
					t.Errorf("profile %q", options.Profile)
				}
				return fetch.Document{HTML: `<p>rendered@example.org</p>`, FinalURL: target, StatusCode: 200, Type: fetch.ContentHTML}, nil
			})
			var protected []string
			if test.protected {
				protected = []string{"example.org"}
			}
			loader := NewLoader(http, browser, nil, fetch.NewRoutes(time.Hour, protected))
			_, err := loader.Load(context.Background(), "https://example.org/", Options{Render: "auto", Profile: test.profile})
			if err != nil {
				t.Fatal(err)
			}
			if calls[len(calls)-1] != test.want {
				t.Fatalf("calls: %v", calls)
			}
			if (test.protected || test.profile != "") && len(calls) != 1 {
				t.Fatalf("extra loads: %v", calls)
			}
		})
	}
}
