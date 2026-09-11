package rodfetch

import (
	"context"
	"fmt"
	"net/url"
	"sync"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/proto"
	"github.com/simonbalfe/pagelode/internal/page"
)

type Engine struct {
	mu      sync.Mutex
	browser *rod.Browser
	launch  *launcher.Launcher
	proxy   *url.URL
}

func New(proxyURL string) (*Engine, error) {
	if proxyURL == "" {
		return &Engine{}, nil
	}
	proxy, err := url.Parse(proxyURL)
	if err != nil {
		return nil, fmt.Errorf("rod: parse proxy URL: %w", err)
	}
	return &Engine{proxy: proxy}, nil
}

func (e *Engine) Fetch(ctx context.Context, targetURL string, session page.Session) (page.Document, error) {
	browser, err := e.connect()
	if err != nil {
		return page.Document{}, err
	}

	requestBrowser := browser.Context(ctx)
	tab, err := requestBrowser.Page(proto.TargetCreateTarget{})
	if err != nil {
		return page.Document{}, fmt.Errorf("rod: create page: %w", err)
	}
	defer func() { _ = tab.Close() }()

	if session.UserAgent != "" {
		if err := (proto.EmulationSetUserAgentOverride{
			UserAgent:      session.UserAgent,
			AcceptLanguage: "en-GB,en;q=0.9",
			Platform:       "MacIntel",
		}).Call(tab); err != nil {
			return page.Document{}, fmt.Errorf("rod: set user agent: %w", err)
		}
	}
	if cookies := cookieParams(session.Cookies); len(cookies) > 0 {
		if err := tab.SetCookies(cookies); err != nil {
			return page.Document{}, fmt.Errorf("rod: seed cookies: %w", err)
		}
	}
	if err := tab.Navigate(targetURL); err != nil {
		return page.Document{}, fmt.Errorf("rod: navigate: %w", err)
	}
	if err := tab.WaitLoad(); err != nil {
		return page.Document{}, fmt.Errorf("rod: wait for load: %w", err)
	}
	if err := tab.WaitStable(350 * time.Millisecond); err != nil && ctx.Err() != nil {
		return page.Document{}, fmt.Errorf("rod: wait for stability: %w", ctx.Err())
	}

	info, err := tab.Info()
	if err != nil {
		return page.Document{}, fmt.Errorf("rod: page info: %w", err)
	}
	html, err := tab.HTML()
	if err != nil {
		return page.Document{}, fmt.Errorf("rod: read HTML: %w", err)
	}

	return page.Document{
		Provider: page.ProviderRod,
		FinalURL: info.URL,
		Title:    info.Title,
		HTML:     html,
		Type:     page.ContentHTML,
		Session:  session,
	}, nil
}

func (e *Engine) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.browser == nil {
		return nil
	}
	err := e.browser.Close()
	e.browser = nil
	if e.launch != nil {
		e.launch.Kill()
		e.launch = nil
	}
	if err != nil {
		return fmt.Errorf("rod: close browser: %w", err)
	}
	return nil
}

func (e *Engine) connect() (*rod.Browser, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.browser != nil {
		return e.browser, nil
	}

	launch := launcher.New().Headless(true).Leakless(true)
	if e.proxy != nil {
		proxy := *e.proxy
		proxy.User = nil
		launch.Proxy(proxy.String())
	}
	controlURL, err := launch.Launch()
	if err != nil {
		return nil, fmt.Errorf("rod: launch browser: %w", err)
	}
	browser := rod.New().ControlURL(controlURL)
	if err := browser.Connect(); err != nil {
		launch.Kill()
		return nil, fmt.Errorf("rod: connect browser: %w", err)
	}
	e.launch = launch
	e.browser = browser
	return browser, nil
}

func cookieParams(cookies []page.Cookie) []*proto.NetworkCookieParam {
	result := make([]*proto.NetworkCookieParam, 0, len(cookies))
	for _, cookie := range cookies {
		result = append(result, &proto.NetworkCookieParam{
			Name:     cookie.Name,
			Value:    cookie.Value,
			Domain:   cookie.Domain,
			Path:     cookie.Path,
			Secure:   cookie.Secure,
			HTTPOnly: cookie.HTTPOnly,
			SameSite: proto.NetworkCookieSameSite(cookie.SameSite),
			Expires:  proto.TimeSinceEpoch(cookie.Expires),
		})
	}
	return result
}
