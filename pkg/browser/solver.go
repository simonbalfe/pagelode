package captcha

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/PuerkitoBio/goquery"
	"github.com/simonbalfe/pagelode/pkg/capsolver"
	"github.com/simonbalfe/pagelode/pkg/httpfetch"
	"github.com/simonbalfe/pagelode/pkg/page"
)

const googleExemption = "GOOGLE_ABUSE_EXEMPTION"

type Renderer interface {
	Fetch(context.Context, string, page.Session) (page.Document, error)
	Capture(context.Context, string, page.Session, page.CaptureOptions) (page.Document, error)
}

type Tasks interface {
	Recaptcha(context.Context, capsolver.RecaptchaTask) (string, error)
	DataDome(context.Context, capsolver.DataDomeTask) (string, error)
}

type Submitter interface {
	Submit(context.Context, httpfetch.Form, page.Session) (page.Document, error)
}

type Solver struct {
	renderer  Renderer
	tasks     Tasks
	submitter Submitter
}

func New(renderer Renderer, tasks Tasks, submitter Submitter) *Solver {
	return &Solver{renderer: renderer, tasks: tasks, submitter: submitter}
}

func (s *Solver) Fetch(ctx context.Context, targetURL string, session page.Session) (page.Document, error) {
	return s.render(ctx, session, func(session page.Session) (page.Document, error) {
		return s.renderer.Fetch(ctx, targetURL, session)
	})
}

func (s *Solver) Capture(ctx context.Context, targetURL string, session page.Session, options page.CaptureOptions) (page.Document, error) {
	return s.render(ctx, session, func(session page.Session) (page.Document, error) {
		return s.renderer.Capture(ctx, targetURL, session, options)
	})
}

func (s *Solver) render(ctx context.Context, session page.Session, load func(page.Session) (page.Document, error)) (page.Document, error) {
	document, err := load(session)
	if err != nil || s.tasks == nil {
		return document, err
	}
	cookies, err := s.solve(ctx, document)
	if err != nil {
		return page.Document{}, err
	}
	if len(cookies) == 0 {
		return document, nil
	}
	session.Cookies = merge(session.Cookies, cookies)
	return load(session)
}

func (s *Solver) solve(ctx context.Context, document page.Document) ([]page.Cookie, error) {
	challenge := document.Challenge
	if challenge == nil {
		return nil, nil
	}
	location, err := url.Parse(document.FinalURL)
	if err != nil {
		return nil, fmt.Errorf("captcha: parse final URL: %w", err)
	}
	switch {
	case challenge.Vendor == page.ChallengeRecaptcha && strings.HasPrefix(location.Hostname(), "www.google.") && strings.HasPrefix(location.Path, "/sorry"):
		return s.solveGoogle(ctx, document, *challenge, location)
	case challenge.Vendor == page.ChallengeDataDome:
		return s.solveDataDome(ctx, document, *challenge, location)
	default:
		return nil, nil
	}
}

func (s *Solver) solveGoogle(ctx context.Context, document page.Document, challenge page.Challenge, location *url.URL) ([]page.Cookie, error) {
	if challenge.SiteKey == "" {
		return nil, nil
	}
	html, err := goquery.NewDocumentFromReader(strings.NewReader(document.HTML))
	if err != nil {
		return nil, fmt.Errorf("google captcha: parse page: %w", err)
	}
	form := html.Find("textarea[name='g-recaptcha-response']").First().Closest("form")
	if form.Length() == 0 {
		form = html.Find("form").First()
	}
	if form.Length() == 0 {
		return nil, errors.New("google captcha: form not found")
	}
	action, err := location.Parse(form.AttrOr("action", ""))
	if err != nil {
		return nil, fmt.Errorf("google captcha: parse form action: %w", err)
	}
	token, err := s.tasks.Recaptcha(ctx, capsolver.RecaptchaTask{
		WebsiteURL: document.FinalURL,
		WebsiteKey: challenge.SiteKey,
		DataS:      challenge.DataS,
		ProxyURL:   document.Session.ProxyURL,
	})
	if err != nil {
		return nil, fmt.Errorf("google captcha: %w", err)
	}
	values := formValues(form)
	values.Set("g-recaptcha-response", token)
	submitted, err := s.submitter.Submit(ctx, httpfetch.Form{
		Method:  form.AttrOr("method", ""),
		Action:  action.String(),
		Values:  values,
		Referer: document.FinalURL,
	}, document.Session)
	if err != nil {
		return nil, fmt.Errorf("google captcha: submit answer: %w", err)
	}
	granted := changedCookies(document.Session.Cookies, submitted.Session.Cookies)
	if !hasCookie(granted, googleExemption) {
		return nil, fmt.Errorf("google captcha: answer rejected with HTTP %d", submitted.StatusCode)
	}
	return granted, nil
}

func (s *Solver) solveDataDome(ctx context.Context, document page.Document, challenge page.Challenge, location *url.URL) ([]page.Cookie, error) {
	captchaURL := challenge.URL
	if captchaURL == "" || document.Session.ProxyURL == "" {
		return nil, nil
	}
	captcha, err := url.Parse(captchaURL)
	if err != nil {
		return nil, fmt.Errorf("datadome captcha: parse captcha URL: %w", err)
	}
	if captcha.Query().Get("t") == "bv" {
		return nil, nil
	}
	solved, err := s.tasks.DataDome(ctx, capsolver.DataDomeTask{
		CaptchaURL: captchaURL,
		UserAgent:  document.Session.UserAgent,
		ProxyURL:   document.Session.ProxyURL,
	})
	if err != nil {
		return nil, fmt.Errorf("datadome captcha: %w", err)
	}
	cookie, err := http.ParseSetCookie(solved)
	if err != nil {
		return nil, fmt.Errorf("datadome captcha: parse solved cookie: %w", err)
	}
	return []page.Cookie{{Name: cookie.Name, Value: cookie.Value, Domain: location.Hostname(), Path: "/"}}, nil
}

func formValues(form *goquery.Selection) url.Values {
	values := url.Values{}
	form.Find("input[name]").Each(func(_ int, input *goquery.Selection) {
		switch strings.ToLower(input.AttrOr("type", "text")) {
		case "hidden", "text":
			values.Add(input.AttrOr("name", ""), input.AttrOr("value", ""))
		}
	})
	return values
}

func changedCookies(before []page.Cookie, after []page.Cookie) []page.Cookie {
	known := make(map[string]string, len(before))
	for _, cookie := range before {
		known[cookie.Name] = cookie.Value
	}
	var changed []page.Cookie
	for _, cookie := range after {
		if value, ok := known[cookie.Name]; !ok || value != cookie.Value {
			changed = append(changed, cookie)
		}
	}
	return changed
}

func merge(base []page.Cookie, solved []page.Cookie) []page.Cookie {
	merged := append([]page.Cookie(nil), base...)
	indexes := make(map[string]int, len(merged))
	for index, cookie := range merged {
		indexes[cookie.Name] = index
	}
	for _, cookie := range solved {
		if index, ok := indexes[cookie.Name]; ok {
			merged[index] = cookie
			continue
		}
		indexes[cookie.Name] = len(merged)
		merged = append(merged, cookie)
	}
	return merged
}

func hasCookie(cookies []page.Cookie, name string) bool {
	for _, cookie := range cookies {
		if cookie.Name == name {
			return true
		}
	}
	return false
}
