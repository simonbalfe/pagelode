package emails

import (
	"context"
	"errors"
	"net/url"
	"strings"

	"github.com/PuerkitoBio/goquery"
	"github.com/simonbalfe/pagelode/internal/classify"
	"github.com/simonbalfe/pagelode/internal/limit"
	"github.com/simonbalfe/pagelode/internal/memory"
	"github.com/simonbalfe/pagelode/internal/orchestrator"
	"github.com/simonbalfe/pagelode/internal/page"
)

type Capturer interface {
	Capture(context.Context, string, page.Session, page.CaptureOptions) (page.Document, error)
}

type Loader struct {
	http           orchestrator.Fetcher
	chrome         Capturer
	patchright     Capturer
	capsolver      orchestrator.Fetcher
	routes         *memory.Routes
	browserLimiter *limit.Limiter
	chromeEnabled  bool
}

func NewLoader(http orchestrator.Fetcher, chrome, patchright Capturer, capsolver orchestrator.Fetcher, routes *memory.Routes, browserLimiter *limit.Limiter, chromeEnabled bool) *Loader {
	return &Loader{http: http, chrome: chrome, patchright: patchright, capsolver: capsolver, routes: routes, browserLimiter: browserLimiter, chromeEnabled: chromeEnabled}
}

func (l *Loader) Load(ctx context.Context, target string, options Options) (page.Document, error) {
	if options.ProfileDirectory != "" {
		return l.browser(ctx, l.patchright, target, page.Session{}, options)
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
			classification := classify.Document(document)
			if classification.Kind == classify.Blocked {
				preferred = page.ProviderPatchright
			}
			if classification.Kind == classify.Dead {
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
	if l.chromeEnabled && l.chrome != nil && preferred != page.ProviderPatchright {
		rendered, renderErr := l.browser(ctx, l.chrome, target, document.Session, options)
		if renderErr == nil {
			return rendered, nil
		}
		if ctx.Err() != nil || errors.Is(renderErr, limit.ErrSaturated) {
			return document, renderErr
		}
	}
	rendered, renderErr := l.browser(ctx, l.patchright, target, document.Session, options)
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

func (l *Loader) browser(ctx context.Context, capturer Capturer, target string, session page.Session, options Options) (page.Document, error) {
	if capturer == nil {
		return page.Document{}, errors.New("browser unavailable")
	}
	var document page.Document
	err := l.browserLimiter.Run(ctx, func() error {
		var err error
		document, err = capturer.Capture(ctx, target, session, page.CaptureOptions{WaitMS: 500, ProfileDirectory: options.ProfileDirectory})
		return err
	})
	if err != nil {
		return document, err
	}
	if !usable(document) {
		return document, errors.New("browser page unavailable")
	}
	if options.ProfileDirectory == "" {
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
