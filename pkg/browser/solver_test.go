package browser

import (
	"context"
	"github.com/simonbalfe/pagelode/pkg/fetch"
	"net/http"
	"testing"
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
	documents []fetch.Document
	sessions  []fetch.Session
}

func (r *renderer) Fetch(_ context.Context, _ string, session fetch.Session) (fetch.Document, error) {
	r.sessions = append(r.sessions, session)
	document := r.documents[0]
	if len(r.documents) > 1 {
		r.documents = r.documents[1:]
	}
	return document, nil
}

func (r *renderer) Capture(ctx context.Context, target string, session fetch.Session, _ fetch.CaptureOptions) (fetch.Document, error) {
	return r.Fetch(ctx, target, session)
}

type tasks struct {
	recaptcha RecaptchaTask
	datadome  DataDomeTask
	calls     int
}

func (t *tasks) Recaptcha(_ context.Context, task RecaptchaTask) (string, error) {
	t.calls++
	t.recaptcha = task
	return "token", nil
}

func (t *tasks) DataDome(_ context.Context, task DataDomeTask) (string, error) {
	t.calls++
	t.datadome = task
	return "datadome=solved; Max-Age=31536000; Domain=.shop.example; Path=/; Secure", nil
}

type submitter struct {
	form    fetch.Form
	cookies []fetch.Cookie
}

func (s *submitter) Submit(_ context.Context, form fetch.Form, session fetch.Session) (fetch.Document, error) {
	s.form = form
	return fetch.Document{StatusCode: http.StatusFound, Session: fetch.Session{Cookies: append(session.Cookies, s.cookies...)}}, nil
}

func browserSession() fetch.Session {
	return fetch.Session{UserAgent: "Chrome", ProxyURL: proxyURL, Cookies: []fetch.Cookie{{Name: "__Secure-ENID", Value: "browser"}}}
}

func TestGoogleSorryIsSolvedOverHTTPAndRendered(t *testing.T) {
	t.Parallel()

	results := &renderer{documents: []fetch.Document{
		{FinalURL: sorryURL, HTML: sorryHTML, Challenge: googleChallenge(), Session: browserSession()},
		{FinalURL: "https://www.google.com/search?q=shoes", HTML: "<html>results</html>"},
	}}
	solver := &tasks{}
	answers := &submitter{cookies: []fetch.Cookie{{Name: googleExemption, Value: "granted", Domain: "www.google.com"}}}

	document, err := NewSolver(results, solver, answers).Capture(context.Background(), "https://www.google.com/search?q=shoes", fetch.Session{}, fetch.CaptureOptions{})
	if err != nil {
		t.Fatalf("Capture() error = %v", err)
	}
	if document.HTML != "<html>results</html>" {
		t.Errorf("document = %q, want the rendered results", document.HTML)
	}
	if want := (RecaptchaTask{WebsiteURL: sorryURL, WebsiteKey: "site-key", DataS: "data-s-value", ProxyURL: proxyURL}); solver.recaptcha != want {
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

	results := &renderer{documents: []fetch.Document{{FinalURL: sorryURL, HTML: sorryHTML, Challenge: googleChallenge(), Session: browserSession()}}}
	_, err := NewSolver(results, &tasks{}, &submitter{}).Fetch(context.Background(), "https://www.google.com/search?q=shoes", fetch.Session{})
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
	results := &renderer{documents: []fetch.Document{
		{FinalURL: "https://shop.example/item", HTML: `<html><body><iframe src="https://geo.captcha-delivery.com/captcha/?initialCid=abc&amp;t=fe"></iframe></body></html>`, Challenge: &fetch.Challenge{Vendor: fetch.ChallengeDataDome, URL: captchaURL}, Session: browserSession()},
		{FinalURL: "https://shop.example/item", HTML: "<html>item</html>"},
	}}
	solver := &tasks{}

	document, err := NewSolver(results, solver, &submitter{}).Fetch(context.Background(), "https://shop.example/item", fetch.Session{Cookies: []fetch.Cookie{{Name: "visit", Value: "1"}}})
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if document.HTML != "<html>item</html>" {
		t.Errorf("document = %q, want the rendered item", document.HTML)
	}
	if want := (DataDomeTask{CaptchaURL: captchaURL, UserAgent: "Chrome", ProxyURL: proxyURL}); solver.datadome != want {
		t.Errorf("task = %#v, want %#v", solver.datadome, want)
	}
	want := []fetch.Cookie{{Name: "visit", Value: "1"}, {Name: "datadome", Value: "solved", Domain: "shop.example", Path: "/"}}
	if got := results.sessions[1].Cookies; len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("second render cookies = %#v, want %#v", got, want)
	}
}

func TestUnsolvablePagesReturnTheFirstRender(t *testing.T) {
	t.Parallel()

	banned := &fetch.Challenge{Vendor: fetch.ChallengeDataDome, URL: "https://geo.captcha-delivery.com/captcha/?initialCid=abc&t=bv"}
	slider := &fetch.Challenge{Vendor: fetch.ChallengeDataDome, URL: "https://geo.captcha-delivery.com/captcha/?initialCid=abc&t=fe"}
	for _, test := range []struct {
		name     string
		document fetch.Document
		tasks    Tasks
	}{
		{name: "ordinary page", document: fetch.Document{FinalURL: "https://example.com/", HTML: "<html>ok</html>", Session: browserSession()}, tasks: &tasks{}},
		{name: "banned DataDome IP", document: fetch.Document{FinalURL: "https://shop.example/", Challenge: banned, Session: browserSession()}, tasks: &tasks{}},
		{name: "DataDome without a proxy", document: fetch.Document{FinalURL: "https://shop.example/", Challenge: slider, Session: fetch.Session{UserAgent: "Chrome"}}, tasks: &tasks{}},
		{name: "no CapSolver key", document: fetch.Document{FinalURL: sorryURL, HTML: sorryHTML, Challenge: googleChallenge(), Session: browserSession()}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			results := &renderer{documents: []fetch.Document{test.document}}
			document, err := NewSolver(results, test.tasks, &submitter{}).Fetch(context.Background(), test.document.FinalURL, fetch.Session{})
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

func googleChallenge() *fetch.Challenge {
	return &fetch.Challenge{Vendor: fetch.ChallengeRecaptcha, SiteKey: "site-key", DataS: "data-s-value"}
}
