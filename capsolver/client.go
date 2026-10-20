// Package capsolver solves Cloudflare, reCAPTCHA v2, and DataDome challenges
// through the CapSolver task API and replays the solved session.
package capsolver

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"

	"github.com/simonbalfe/pagelode/page"
)

// Fetcher fetches a page for a session; Client uses one to load the
// challenge page and another to replay the solved session.
type Fetcher interface {
	Fetch(context.Context, string, page.Session) (page.Document, error)
}

// Client fetches Cloudflare-protected pages by solving an AntiCloudflareTask
// and replaying the target with the returned user agent and cookies.
type Client struct {
	api              *API
	proxy            string
	challengeFetcher Fetcher
	solvedFetcher    Fetcher
}

// New returns a Client that solves through api using proxyURL, which must
// carry a username and password. It returns nil, nil when api is nil or
// proxyURL is empty, meaning solving is disabled.
func New(api *API, proxyURL string, challengeFetcher Fetcher, solvedFetcher Fetcher) (*Client, error) {
	if api == nil || proxyURL == "" {
		return nil, nil
	}
	proxy, err := solverProxy(proxyURL)
	if err != nil {
		return nil, fmt.Errorf("capsolver: configure proxy: %w", err)
	}
	return &Client{
		api:              api,
		proxy:            proxy,
		challengeFetcher: challengeFetcher,
		solvedFetcher:    solvedFetcher,
	}, nil
}

// ResolveProxy rewrites the proxy host in raw to its CNAME target, or else to
// its first IPv4 address, so CapSolver connects to the same exit as local
// requests. IP-literal hosts are returned unchanged.
func ResolveProxy(ctx context.Context, raw string) (string, error) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("capsolver: parse proxy: %w", err)
	}
	host := parsed.Hostname()
	if host == "" || parsed.Port() == "" {
		return "", errors.New("capsolver: proxy must include host and port")
	}
	if net.ParseIP(host) != nil {
		return raw, nil
	}
	canonical, canonicalErr := net.DefaultResolver.LookupCNAME(ctx, host)
	canonical = strings.TrimSuffix(canonical, ".")
	if canonicalErr == nil && canonical != "" && !strings.EqualFold(canonical, host) {
		parsed.Host = net.JoinHostPort(canonical, parsed.Port())
		return parsed.String(), nil
	}
	addresses, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return "", fmt.Errorf("capsolver: resolve proxy host: %w", err)
	}
	for _, address := range addresses {
		if ipv4 := address.IP.To4(); ipv4 != nil {
			parsed.Host = net.JoinHostPort(ipv4.String(), parsed.Port())
			return parsed.String(), nil
		}
	}
	return "", errors.New("capsolver: proxy host has no IPv4 address")
}

// Fetch loads targetURL with the challenge fetcher, solves the challenge,
// merges the solution cookies into the session, and refetches with the
// solved fetcher. The returned document's Provider is ProviderCapSolver.
func (c *Client) Fetch(ctx context.Context, targetURL string, session page.Session) (page.Document, error) {
	bootstrap, err := c.challengeFetcher.Fetch(ctx, targetURL, session)
	if err != nil {
		return page.Document{}, fmt.Errorf("capsolver: fetch challenge page: %w", err)
	}
	debug("challenge_fetched", map[string]any{"status": bootstrap.StatusCode, "user_agent_major": chromeMajor(bootstrap.Session.UserAgent), "cookies": len(bootstrap.Session.Cookies)})
	solved, err := c.api.solve(ctx, task{
		Type:       "AntiCloudflareTask",
		WebsiteURL: targetURL,
		Proxy:      c.proxy,
		UserAgent:  bootstrap.Session.UserAgent,
		HTML:       bootstrap.HTML,
	})
	if err != nil {
		return page.Document{}, err
	}
	if solved.UserAgent == "" || len(solved.Cookies) == 0 {
		return page.Document{}, errors.New("capsolver: solution omitted browser session")
	}
	debug("challenge_solved", map[string]any{"user_agent_major": chromeMajor(solved.UserAgent), "cookies": len(solved.Cookies)})
	parsed, err := url.Parse(targetURL)
	if err != nil {
		return page.Document{}, fmt.Errorf("capsolver: parse target URL: %w", err)
	}
	session = bootstrap.Session
	session.UserAgent = solved.UserAgent
	cookieIndexes := make(map[string]int, len(session.Cookies))
	for index, cookie := range session.Cookies {
		cookieIndexes[cookie.Name] = index
	}
	for name, value := range solved.Cookies {
		cookie := page.Cookie{
			Name:   name,
			Value:  value,
			Domain: parsed.Hostname(),
			Path:   "/",
		}
		if index, ok := cookieIndexes[name]; ok {
			session.Cookies[index] = cookie
		} else {
			session.Cookies = append(session.Cookies, cookie)
		}
	}
	document, err := c.solvedFetcher.Fetch(ctx, targetURL, session)
	if err != nil {
		return page.Document{}, fmt.Errorf("capsolver: fetch solved page: %w", err)
	}
	document.Provider = page.ProviderCapSolver
	return document, nil
}

func chromeMajor(userAgent string) string {
	const marker = "Chrome/"
	start := strings.Index(userAgent, marker)
	if start < 0 {
		return ""
	}
	version := userAgent[start+len(marker):]
	if end := strings.IndexByte(version, '.'); end >= 0 {
		return version[:end]
	}
	return version
}

func solverProxy(raw string) (string, error) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	if parsed.User == nil {
		return "", errors.New("proxy must include username and password")
	}
	password, ok := parsed.User.Password()
	if !ok || parsed.User.Username() == "" || password == "" {
		return "", errors.New("proxy must include username and password")
	}
	return taskProxy(raw)
}
