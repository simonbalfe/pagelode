package chromefetch

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sync"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
	"github.com/simonbalfe/pagelode/internal/page"
)

type Engine struct {
	mu      sync.Mutex
	browser context.Context
	cancel  context.CancelFunc
	proxy   *url.URL
}

func New(proxyURL string) (*Engine, error) {
	if proxyURL == "" {
		return &Engine{}, nil
	}
	proxy, err := url.Parse(proxyURL)
	if err != nil {
		return nil, fmt.Errorf("chromedp: parse proxy URL: %w", err)
	}
	if proxy.User != nil {
		if _, ok := proxy.User.Password(); !ok {
			return nil, errors.New("chromedp: proxy password is required")
		}
	}
	return &Engine{proxy: proxy}, nil
}

func (e *Engine) Fetch(ctx context.Context, targetURL string, session page.Session) (page.Document, error) {
	return e.render(ctx, targetURL, session, nil)
}

func (e *Engine) Capture(ctx context.Context, targetURL string, session page.Session, options page.CaptureOptions) (page.Document, error) {
	return e.render(ctx, targetURL, session, &options)
}

func (e *Engine) render(ctx context.Context, targetURL string, session page.Session, options *page.CaptureOptions) (page.Document, error) {
	browser, err := e.connect(ctx)
	if err != nil {
		return page.Document{}, err
	}
	tab, cancel := chromedp.NewContext(browser)
	defer cancel()
	stop := context.AfterFunc(ctx, cancel)
	defer stop()

	var actions chromedp.Tasks
	var capture *collector
	if options != nil {
		capture = newCollector()
		chromedp.ListenTarget(tab, capture.listen)
		actions = append(actions, network.Enable().WithMaxTotalBufferSize(page.MaxCaptureTotalBytes).WithMaxResourceBufferSize(page.MaxCaptureBodyBytes).WithMaxPostDataSize(page.MaxCaptureBodyBytes), network.SetCacheDisabled(true))
	}
	if session.UserAgent != "" {
		actions = append(actions, emulation.SetUserAgentOverride(session.UserAgent).WithAcceptLanguage("en-GB,en;q=0.9").WithPlatform("MacIntel"))
	}
	if cookies := cookieParams(targetURL, session.Cookies); len(cookies) > 0 {
		actions = append(actions, network.SetCookies(cookies))
	}
	proxyActions, authErrors := e.proxyActions(tab)
	actions = append(actions, proxyActions...)
	var html, title, finalURL string
	wait := 350 * time.Millisecond
	if options != nil {
		wait = time.Duration(options.WaitMS) * time.Millisecond
	}
	actions = append(actions,
		chromedp.Navigate(targetURL),
		chromedp.Sleep(wait),
		chromedp.Location(&finalURL),
		chromedp.Title(&title),
		chromedp.OuterHTML("html", &html, chromedp.ByQuery),
	)
	if err := chromedp.Run(tab, actions); err != nil {
		return page.Document{}, fmt.Errorf("chromedp: render page: %w", err)
	}
	if authErrors != nil {
		select {
		case err := <-authErrors:
			return page.Document{}, fmt.Errorf("chromedp: proxy authentication: %w", err)
		default:
		}
	}
	document := page.Document{Provider: page.ProviderChromedp, FinalURL: finalURL, Title: title, HTML: html, Type: page.ContentHTML, Session: session}
	if capture != nil {
		document.Traffic = capture.snapshot(tab)
		capturedSession, err := browserSession(tab)
		if err != nil {
			return page.Document{}, fmt.Errorf("chromedp: capture session: %w", err)
		}
		document.Session = capturedSession
		for _, entry := range document.Traffic.Entries {
			if entry.ResourceType == "Document" && entry.URL == finalURL {
				document.StatusCode = entry.Status
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return page.Document{}, err
	}
	return document, nil
}

func (e *Engine) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.cancel != nil {
		e.cancel()
		e.cancel = nil
		e.browser = nil
	}
	return nil
}

func (e *Engine) connect(ctx context.Context) (context.Context, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.browser != nil {
		return e.browser, nil
	}
	options := append([]chromedp.ExecAllocatorOption{}, chromedp.DefaultExecAllocatorOptions[:]...)
	if e.proxy != nil {
		proxy := *e.proxy
		proxy.User = nil
		options = append(options, chromedp.ProxyServer(proxy.String()), chromedp.Flag("proxy-bypass-list", "<-loopback>"))
	}
	allocator, cancelAllocator := chromedp.NewExecAllocator(context.Background(), options...)
	browser, cancelBrowser := chromedp.NewContext(allocator)
	stop := context.AfterFunc(ctx, cancelBrowser)
	err := chromedp.Run(browser)
	stop()
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		cancelBrowser()
		cancelAllocator()
		return nil, fmt.Errorf("chromedp: launch browser: %w", err)
	}
	e.browser = browser
	e.cancel = func() { cancelBrowser(); cancelAllocator() }
	return browser, nil
}

func cookieParams(targetURL string, cookies []page.Cookie) []*network.CookieParam {
	result := make([]*network.CookieParam, 0, len(cookies))
	for _, cookie := range cookies {
		param := &network.CookieParam{Name: cookie.Name, Value: cookie.Value, Domain: cookie.Domain, Path: cookie.Path, Secure: cookie.Secure, HTTPOnly: cookie.HTTPOnly}
		if cookie.Domain == "" {
			param.URL = targetURL
		}
		if cookie.SameSite != "" {
			param.SameSite = network.CookieSameSite(cookie.SameSite)
		}
		if cookie.Expires > 0 {
			expires := cdp.TimeSinceEpoch(time.Unix(0, int64(cookie.Expires*float64(time.Second))))
			param.Expires = &expires
		}
		result = append(result, param)
	}
	return result
}
