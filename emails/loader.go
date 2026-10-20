package emails

import (
	"context"
	"errors"
	"net/url"
	"strings"

	"github.com/PuerkitoBio/goquery"
	"github.com/simonbalfe/pagelode/classify"
	"github.com/simonbalfe/pagelode/limit"
	"github.com/simonbalfe/pagelode/memory"
	"github.com/simonbalfe/pagelode/orchestrator"
	"github.com/simonbalfe/pagelode/page"
)

type Capturer interface {
	Capture(context.Context, string, page.Session, page.CaptureOptions) (page.Document, error)
}

type Loader struct {
	http      orchestrator.Fetcher
	browser   Capturer
	capsolver orchestrator.Fetcher
	routes    *memory.Routes
}

func NewLoader(http orchestrator.Fetcher, browser Capturer, capsolver orchestrator.Fetcher, routes *memory.Routes) *Loader {
	return &Loader{http: http, browser: browser, capsolver: capsolver, routes: routes}
}

func (l *Loader) Load(ctx context.Context, target string, options Options) (page.Document, error) {
	if options.Profile != "" {
		return l.render(ctx, target, page.Session{}, options)
	}
	parsed, err := url.Parse(target)
	if err != nil {
		return page.Document{}, err
	}
	preferred, _ := l.routes.Preferred(parsed.Hostname())
	var document page.Document
	if options.Render != "always" && (preferred != page.ProviderPatchright || options.Render == "never") {
		document, err = l.http.Fetch(ctx, target, page.Session{})
		if ctx.Err() != nil {
			return page.Document{}, ctx.Err()
		}
		if err == nil {
			if classify.Document(document).Kind == classify.Dead {
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
	if ctx.Err() != nil || errors.Is(renderErr, limit.ErrSaturated) {
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

func (l *Loader) render(ctx context.Context, target string, session page.Session, options Options) (page.Document, error) {
	if l.browser == nil {
		return page.Document{}, errors.New("browser unavailable")
	}
	document, err := l.browser.Capture(ctx, target, session, page.CaptureOptions{WaitMS: 500, Profile: options.Profile})
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

func usable(document page.Document) bool {
	if document.StatusCode >= 400 {
		return false
	}
	kind := classify.Document(document).Kind
	return kind != classify.Blocked && kind != classify.Retryable && kind != classify.Dead
}

func needsBrowser(document page.Document) bool {
	if document.Type != page.ContentHTML || len(extractAddresses(document)) > 0 {
		return false
	}
	if classify.Document(document).Kind == classify.NeedsRender {
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
