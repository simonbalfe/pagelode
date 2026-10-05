package emails

import (
	"context"
	"testing"
	"time"

	"github.com/simonbalfe/pagelode/internal/limit"
	"github.com/simonbalfe/pagelode/internal/memory"
	"github.com/simonbalfe/pagelode/internal/page"
)

type fetchFunc func(context.Context, string, page.Session) (page.Document, error)

func (f fetchFunc) Fetch(ctx context.Context, target string, session page.Session) (page.Document, error) {
	return f(ctx, target, session)
}

type captureFunc func(context.Context, string, page.Session, page.CaptureOptions) (page.Document, error)

func (f captureFunc) Capture(ctx context.Context, target string, session page.Session, options page.CaptureOptions) (page.Document, error) {
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
		{name: "JavaScript shell", body: `<div id="app"></div><script src="/app.js"></script>`, status: 200, want: "chrome"},
		{name: "blocked", body: `<p>Access denied</p>`, status: 403, want: "patchright"},
		{name: "protected", protected: true, want: "patchright"},
		{name: "profile", profile: "/private/profile", want: "patchright"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var calls []string
			http := fetchFunc(func(context.Context, string, page.Session) (page.Document, error) {
				calls = append(calls, "http")
				return page.Document{HTML: test.body, Type: page.ContentHTML, FinalURL: "https://example.org/", StatusCode: test.status}, nil
			})
			browser := func(name string) captureFunc {
				return func(_ context.Context, target string, _ page.Session, options page.CaptureOptions) (page.Document, error) {
					calls = append(calls, name)
					if options.ProfileDirectory != test.profile {
						t.Errorf("profile %q", options.ProfileDirectory)
					}
					return page.Document{HTML: `<p>rendered@example.org</p>`, FinalURL: target, StatusCode: 200, Type: page.ContentHTML}, nil
				}
			}
			var protected []string
			if test.protected {
				protected = []string{"example.org"}
			}
			loader := NewLoader(http, browser("chrome"), browser("patchright"), nil, memory.NewRoutes(time.Hour, protected), limit.New(1, 10), true)
			_, err := loader.Load(context.Background(), "https://example.org/", Options{Render: "auto", ProfileDirectory: test.profile})
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
