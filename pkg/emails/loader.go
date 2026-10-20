package emails

import (
	"context"
	"errors"
	"net/url"
	"strings"

	"github.com/PuerkitoBio/goquery"
	"github.com/simonbalfe/pagelode/pkg/fetch"
)

type Capturer interface {
	Capture(context.Context, string, fetch.Session, fetch.CaptureOptions) (fetch.Document, error)
}

type Loader struct {
	http      fetch.Fetcher
	browser   Capturer
	capsolver fetch.Fetcher
	routes    *fetch.Routes
}

func NewLoader(http fetch.Fetcher, browser Capturer, capsolver fetch.Fetcher, routes *fetch.Routes) *Loader {
	return &Loader{http: http, browser: browser, capsolver: capsolver, routes: routes}
}

func (l *Loader) Load(ctx context.Context, target string, options Options) (fetch.Document, error) {
	if options.Profile != "" {
		return l.render(ctx, target, fetch.Session{}, options)
	}
	parsed, err := url.Parse(target)
	if err != nil {
		return fetch.Document{}, err
	}
	preferred, _ := l.routes.Preferred(parsed.Hostname())
	var document fetch.Document
	if options.Render != "always" && (preferred != fetch.ProviderPatchright || options.Render == "never") {
		document, err = l.http.Fetch(ctx, target, fetch.Session{})
		if ctx.Err() != nil {
			return fetch.Document{}, ctx.Err()
		}
		if err == nil {
			if fetch.Classify(document).Kind == fetch.Dead {
				return document, nil
			}
			if usable(document) && (options.Render == "never" || !needsBrowser(document)) {
				return document, nil
			}
		}
		if options.Render == "never" {
			if err != nil {
				return document, err
			}
			return document, errors.New("page unavailable over HTTP")
		}
	}
	rendered, renderErr := l.render(ctx, target, document.Session, options)
	if renderErr == nil {
		return rendered, nil
	}
	if ctx.Err() != nil || errors.Is(renderErr, fetch.ErrSaturated) {
		return document, renderErr
	}
	if l.capsolver != nil {
		solved, solveErr := l.capsolver.Fetch(ctx, target, document.Session)
		if solveErr != nil {
			return solved, solveErr
		}
		if usable(solved) {
			return solved, nil
		}
		return solved, errors.New("page unavailable after challenge solver")
	}
	return document, errors.New("page unavailable after browser fallback")
}

func (l *Loader) render(ctx context.Context, target string, session fetch.Session, options Options) (fetch.Document, error) {
	if l.browser == nil {
		return fetch.Document{}, errors.New("browser unavailable")
	}
	document, err := l.browser.Capture(ctx, target, session, fetch.CaptureOptions{WaitMS: 500, Profile: options.Profile})
	if err != nil {
		return document, err
	}
	if !usable(document) {
		return document, errors.New("browser page unavailable")
	}
	if options.Profile == "" {
		if parsed, err := url.Parse(target); err == nil {
			l.routes.Record(parsed.Hostname(), document.Provider)
		}
	}
	return document, nil
}

func usable(document fetch.Document) bool {
	if document.StatusCode >= 400 {
		return false
	}
	kind := fetch.Classify(document).Kind
	return kind != fetch.Blocked && kind != fetch.Retryable && kind != fetch.Dead
}

func needsBrowser(document fetch.Document) bool {
	if document.Type != fetch.ContentHTML || len(extractAddresses(document)) > 0 {
		return false
	}
	if fetch.Classify(document).Kind == fetch.NeedsRender {
		return true
	}
	dom, err := goquery.NewDocumentFromReader(strings.NewReader(document.HTML))
	if err != nil {
		return false
	}
	script := strings.ToLower(dom.Find("script:not([src])").Text())
	if strings.Contains(script, "fetch(") || strings.Contains(script, "xmlhttprequest") || strings.Contains(script, "axios") {
		return true
	}
	return priority(document.FinalURL) > 0 && dom.Find("script[src]").Length() > 0
}
