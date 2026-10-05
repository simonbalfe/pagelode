package emails

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/simonbalfe/pagelode/internal/httpfetch"
	"github.com/simonbalfe/pagelode/internal/limit"
	"github.com/simonbalfe/pagelode/internal/memory"
	"github.com/simonbalfe/pagelode/internal/page"
	"github.com/simonbalfe/pagelode/internal/profile"
)

type loadFunc func(context.Context, string, Options) (page.Document, error)

func (f loadFunc) Load(ctx context.Context, target string, options Options) (page.Document, error) {
	return f(ctx, target, options)
}

func TestCrawlPrioritizesContactsAndRetainsSources(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			fmt.Fprint(w, `<html><body><a href="/blog">Blog</a><a href="/contact?token=private">Contact</a><a href="https://other.test/contact">Outside</a><a href="/logout">Logout</a><footer>shared@example.org</footer></body></html>`)
		case "/sitemap.xml":
			w.Header().Set("Content-Type", "application/xml")
			fmt.Fprint(w, `<urlset><url><loc>/team</loc></url><url><loc>/blog</loc></url></urlset>`)
		case "/contact":
			fmt.Fprint(w, `<footer><a href="mailto:shared@example.org">Email</a>contact@example.org</footer>`)
		case "/team":
			fmt.Fprint(w, `<p>team@example.org</p>`)
		default:
			t.Errorf("unexpected page %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	loader := NewLoader(httpfetch.New(""), nil, nil, nil, memory.NewRoutes(time.Hour, nil), limit.New(1, 10), false)
	service := New(loader, limit.New(1, 10), 1, t.TempDir())
	report, err := service.Find(context.Background(), Request{URL: server.URL, MaxPages: 3, Render: "never"})
	if err != nil {
		t.Fatal(err)
	}
	if report.Summary.PagesVisited != 3 || report.Summary.EmailsFound != 3 || !report.Summary.Limited {
		t.Fatalf("report: %#v", report)
	}
	if !strings.Contains(report.Pages[1].URL, "/contact") || !strings.Contains(report.Pages[2].URL, "/team") {
		t.Fatalf("page order: %#v", report.Pages)
	}
	for _, address := range report.Emails {
		if address.Address == "shared@example.org" && len(address.Sources) != 2 {
			t.Errorf("sources: %#v", address.Sources)
		}
		for _, source := range address.Sources {
			if strings.Contains(source.URL, "private") {
				t.Fatal("query secret in source")
			}
		}
	}
}

func TestLimitsAndCancellation(t *testing.T) {
	loader := loadFunc(func(ctx context.Context, target string, _ Options) (page.Document, error) {
		return page.Document{FinalURL: target, HTML: `<p>a@example.org b@example.org c@example.org</p><a href="/contact">Contact</a>`, StatusCode: 200}, nil
	})
	service := New(loader, limit.New(4, 10), 4, t.TempDir())
	report, err := service.Find(context.Background(), Request{URL: "example.org", MaxPages: 1, MaxEmails: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Emails) != 1 || len(report.Pages) != 1 || report.Outcome != "partial" {
		t.Fatalf("report: %#v", report)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := service.Find(ctx, Request{URL: "example.org"}); err != context.Canceled {
		t.Fatalf("cancel: %v", err)
	}
}

func TestDurationReturnsPartialResults(t *testing.T) {
	loader := loadFunc(func(ctx context.Context, target string, _ Options) (page.Document, error) {
		if strings.HasSuffix(target, "/") {
			return page.Document{FinalURL: target, HTML: `<p>saved@example.org</p><a href="/contact">Contact</a>`, StatusCode: 200}, nil
		}
		<-ctx.Done()
		return page.Document{}, ctx.Err()
	})
	service := New(loader, limit.New(4, 10), 1, t.TempDir())
	report, err := service.Find(context.Background(), Request{URL: "example.org", MaxDurationMS: 1000})
	if err != nil {
		t.Fatal(err)
	}
	if report.Outcome != "partial" || len(report.Emails) != 1 || !strings.Contains(strings.Join(report.Warnings, ","), "duration") {
		t.Fatalf("report: %#v", report)
	}
}

func TestProfileLoadsSeriallyAndSkipsPublicSitemap(t *testing.T) {
	root := t.TempDir()
	directory, err := profile.Directory(root, "account", true)
	if err != nil {
		t.Fatal(err)
	}
	var active atomic.Int32
	loader := loadFunc(func(_ context.Context, target string, options Options) (page.Document, error) {
		if active.Add(1) != 1 {
			t.Error("profile loaded concurrently")
		}
		defer active.Add(-1)
		if options.ProfileDirectory != directory {
			t.Errorf("profile options: %#v", options)
		}
		if strings.Contains(target, "sitemap") {
			t.Error("profile used public sitemap")
		}
		body := `<p>private@example.org</p>`
		if strings.HasSuffix(target, "/") {
			body += `<a href="/contact">Contact</a><a href="/team">Team</a>`
		}
		return page.Document{FinalURL: target, HTML: body, StatusCode: 200}, nil
	})
	report, err := New(loader, limit.New(4, 10), 4, root).Find(context.Background(), Request{URL: "example.org", Profile: "account"})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Pages) != 3 || len(report.Emails) != 1 || len(report.Emails[0].Sources) != 3 {
		t.Fatalf("report: %#v", report)
	}
}

func TestRejectOutsideRedirect(t *testing.T) {
	loader := loadFunc(func(context.Context, string, Options) (page.Document, error) {
		return page.Document{FinalURL: "https://outside.test/", HTML: `<p>outside@example.org</p>`, StatusCode: 200}, nil
	})
	report, err := New(loader, limit.New(1, 10), 1, t.TempDir()).Find(context.Background(), Request{URL: "example.org", MaxPages: 1})
	if err != nil {
		t.Fatal(err)
	}
	if report.Outcome != "failed" || len(report.Emails) != 0 {
		t.Fatalf("report: %#v", report)
	}
}
