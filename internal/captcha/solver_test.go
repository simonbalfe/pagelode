package captcha

import (
	"context"
	"net/http"
	"testing"

	"github.com/simonbalfe/pagelode/internal/capsolver"
	"github.com/simonbalfe/pagelode/internal/httpfetch"
	"github.com/simonbalfe/pagelode/internal/page"
)

const (
	sorryURL  = "https://www.google.com/sorry/index?continue=https://www.google.com/search%3Fq%3Dshoes&q=EgQ"
	sorryHTML = `<html><body><form id="captcha-form" action="index" method="post">
<div class="g-recaptcha" data-sitekey="site-key" data-s="data-s-value"></div>
<textarea name="g-recaptcha-response"></textarea>
<input type="hidden" name="q" value="EgQ">
<input type="hidden" name="continue" value="https://www.google.com/search?q=shoes">
<input type="submit" name="submit" value="Submit">
</form></body></html>`
	proxyURL = "http://user:pass@proxy.example:8080"
)

type renderer struct {
	documents []page.Document
	sessions  []page.Session
}

func (r *renderer) Fetch(_ context.Context, _ string, session page.Session) (page.Document, error) {
	r.sessions = append(r.sessions, session)
	document := r.documents[0]
	if len(r.documents) > 1 {
		r.documents = r.documents[1:]
	}
	return document, nil
}

func (r *renderer) Capture(ctx context.Context, target string, session page.Session, _ page.CaptureOptions) (page.Document, error) {
	return r.Fetch(ctx, target, session)
}

type tasks struct {
	recaptcha capsolver.RecaptchaTask
	datadome  capsolver.DataDomeTask
	calls     int
}

func (t *tasks) Recaptcha(_ context.Context, task capsolver.RecaptchaTask) (string, error) {
	t.calls++
	t.recaptcha = task
	return "token", nil
}

func (t *tasks) DataDome(_ context.Context, task capsolver.DataDomeTask) (string, error) {
	t.calls++
	t.datadome = task
	return "datadome=solved; Max-Age=31536000; Domain=.shop.example; Path=/; Secure", nil
}

type submitter struct {
	form    httpfetch.Form
	cookies []page.Cookie
}

func (s *submitter) Submit(_ context.Context, form httpfetch.Form, session page.Session) (page.Document, error) {
	s.form = form
	return page.Document{StatusCode: http.StatusFound, Session: page.Session{Cookies: append(session.Cookies, s.cookies...)}}, nil
}

func browserSession() page.Session {
	return page.Session{UserAgent: "Chrome", ProxyURL: proxyURL, Cookies: []page.Cookie{{Name: "__Secure-ENID", Value: "browser"}}}
}

func TestGoogleSorryIsSolvedOverHTTPAndRendered(t *testing.T) {
	t.Parallel()

	results := &renderer{documents: []page.Document{
		{FinalURL: sorryURL, HTML: sorryHTML, Session: browserSession()},
		{FinalURL: "https://www.google.com/search?q=shoes", HTML: "<html>results</html>"},
	}}
	solver := &tasks{}
	answers := &submitter{cookies: []page.Cookie{{Name: googleExemption, Value: "granted", Domain: "www.google.com"}}}

	document, err := New(results, solver, answers).Capture(context.Background(), "https://www.google.com/search?q=shoes", page.Session{}, page.CaptureOptions{})
	if err != nil {
		t.Fatalf("Capture() error = %v", err)
	}
	if document.HTML != "<html>results</html>" {
		t.Errorf("document = %q, want the rendered results", document.HTML)
	}
	if want := (capsolver.RecaptchaTask{WebsiteURL: sorryURL, WebsiteKey: "site-key", DataS: "data-s-value", ProxyURL: proxyURL}); solver.recaptcha != want {
		t.Errorf("task = %#v, want %#v", solver.recaptcha, want)
	}
	if answers.form.Method != "post" || answers.form.Action != "https://www.google.com/sorry/index" || answers.form.Referer != sorryURL {
		t.Errorf("form = %#v", answers.form)
	}
	if got := answers.form.Values; got.Get("g-recaptcha-response") != "token" || got.Get("q") != "EgQ" || got.Get("continue") != "https://www.google.com/search?q=shoes" || got.Has("submit") {
		t.Errorf("form values = %v", got)
	}
	if len(results.sessions) != 2 {
		t.Fatalf("renders = %d, want 2", len(results.sessions))
	}
	if cookies := results.sessions[1].Cookies; len(cookies) != 1 || cookies[0].Name != googleExemption {
		t.Errorf("second render cookies = %#v, want only the exemption", cookies)
	}
}

func TestGoogleSorryFailsWhenTheAnswerIsRejected(t *testing.T) {
	t.Parallel()

	results := &renderer{documents: []page.Document{{FinalURL: sorryURL, HTML: sorryHTML, Session: browserSession()}}}
	_, err := New(results, &tasks{}, &submitter{}).Fetch(context.Background(), "https://www.google.com/search?q=shoes", page.Session{})
	if err == nil {
		t.Fatal("Fetch() error = nil, want rejected answer")
	}
	if len(results.sessions) != 1 {
		t.Errorf("renders = %d, want 1", len(results.sessions))
	}
}

func TestDataDomeSliderIsSolvedAndRendered(t *testing.T) {
	t.Parallel()

	captchaURL := "https://geo.captcha-delivery.com/captcha/?initialCid=abc&t=fe"
	results := &renderer{documents: []page.Document{
		{FinalURL: "https://shop.example/item", HTML: `<html><body><iframe src="https://geo.captcha-delivery.com/captcha/?initialCid=abc&amp;t=fe"></iframe></body></html>`, Session: browserSession()},
		{FinalURL: "https://shop.example/item", HTML: "<html>item</html>"},
	}}
	solver := &tasks{}

	document, err := New(results, solver, &submitter{}).Fetch(context.Background(), "https://shop.example/item", page.Session{Cookies: []page.Cookie{{Name: "visit", Value: "1"}}})
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if document.HTML != "<html>item</html>" {
		t.Errorf("document = %q, want the rendered item", document.HTML)
	}
	if want := (capsolver.DataDomeTask{CaptchaURL: captchaURL, UserAgent: "Chrome", ProxyURL: proxyURL}); solver.datadome != want {
		t.Errorf("task = %#v, want %#v", solver.datadome, want)
	}
	want := []page.Cookie{{Name: "visit", Value: "1"}, {Name: "datadome", Value: "solved", Domain: "shop.example", Path: "/"}}
	if got := results.sessions[1].Cookies; len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("second render cookies = %#v, want %#v", got, want)
	}
}

func TestUnsolvablePagesReturnTheFirstRender(t *testing.T) {
	t.Parallel()

	banned := `<html><iframe src="https://geo.captcha-delivery.com/captcha/?initialCid=abc&t=bv"></iframe></html>`
	slider := `<html><iframe src="https://geo.captcha-delivery.com/captcha/?initialCid=abc&t=fe"></iframe></html>`
	for _, test := range []struct {
		name     string
		document page.Document
		tasks    Tasks
	}{
		{name: "ordinary page", document: page.Document{FinalURL: "https://example.com/", HTML: "<html>ok</html>", Session: browserSession()}, tasks: &tasks{}},
		{name: "banned DataDome IP", document: page.Document{FinalURL: "https://shop.example/", HTML: banned, Session: browserSession()}, tasks: &tasks{}},
		{name: "DataDome without a proxy", document: page.Document{FinalURL: "https://shop.example/", HTML: slider, Session: page.Session{UserAgent: "Chrome"}}, tasks: &tasks{}},
		{name: "no CapSolver key", document: page.Document{FinalURL: sorryURL, HTML: sorryHTML, Session: browserSession()}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			results := &renderer{documents: []page.Document{test.document}}
			document, err := New(results, test.tasks, &submitter{}).Fetch(context.Background(), test.document.FinalURL, page.Session{})
			if err != nil {
				t.Fatalf("Fetch() error = %v", err)
			}
			if document.HTML != test.document.HTML || len(results.sessions) != 1 {
				t.Errorf("renders = %d, document = %q; want the first render only", len(results.sessions), document.HTML)
			}
			if recorded, ok := test.tasks.(*tasks); ok && recorded.calls != 0 {
				t.Errorf("CapSolver calls = %d, want 0", recorded.calls)
			}
		})
	}
}
