package httpfetch

import (
	"context"
	"fmt"
	"io"
	"mime"
	"net/url"
	"strings"
	"time"

	http "github.com/bogdanfinn/fhttp"
	tlsclient "github.com/bogdanfinn/tls-client"
	"github.com/bogdanfinn/tls-client/profiles"
	"github.com/simonbalfe/pagelode/internal/page"
)

const (
	maxResponseBytes = 12 << 20
	userAgent        = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/150.0.0.0 Safari/537.36"
)

type Client struct {
	proxyURL string
}

func New(proxyURL string) *Client {
	return &Client{proxyURL: proxyURL}
}

func (c *Client) Fetch(ctx context.Context, targetURL string, session page.Session) (page.Document, error) {
	jar := tlsclient.NewCookieJar()
	profile := profiles.Chrome_150
	if session.Replayable() {
		profile = profiles.Chrome_152
	}
	options := []tlsclient.HttpClientOption{
		tlsclient.WithTimeoutSeconds(30),
		tlsclient.WithClientProfile(profile),
		tlsclient.WithCookieJar(jar),
	}
	proxyURL := c.proxyURL
	if session.ProxyURL != "" {
		proxyURL = session.ProxyURL
	}
	if proxyURL != "" {
		options = append(options, tlsclient.WithProxyUrl(proxyURL))
	}
	client, err := tlsclient.NewHttpClient(tlsclient.NewNoopLogger(), options...)
	if err != nil {
		return page.Document{}, fmt.Errorf("http fetch: create TLS client: %w", err)
	}
	defer client.CloseIdleConnections()
	parsedTargetURL, err := url.Parse(targetURL)
	if err != nil {
		return page.Document{}, fmt.Errorf("http fetch: parse target URL: %w", err)
	}
	if len(session.Cookies) > 0 {
		jar.SetCookies(parsedTargetURL, requestCookies(session.Cookies))
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		return page.Document{}, fmt.Errorf("http fetch: create request: %w", err)
	}
	effectiveUserAgent := userAgent
	if session.UserAgent != "" {
		effectiveUserAgent = session.UserAgent
	}
	request.Header = requestHeaders(effectiveUserAgent, session)

	response, err := client.Do(request)
	if err != nil {
		return page.Document{}, fmt.Errorf("http fetch: request: %w", err)
	}
	body, readErr := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	closeErr := response.Body.Close()
	if readErr != nil {
		return page.Document{}, fmt.Errorf("http fetch: read response: %w", readErr)
	}
	if closeErr != nil {
		return page.Document{}, fmt.Errorf("http fetch: close response: %w", closeErr)
	}
	if len(body) > maxResponseBytes {
		return page.Document{}, fmt.Errorf("http fetch: response exceeds %d bytes", maxResponseBytes)
	}

	finalURL := targetURL
	if response.Request != nil && response.Request.URL != nil {
		finalURL = response.Request.URL.String()
	}
	parsedFinalURL, err := url.Parse(finalURL)
	if err != nil {
		return page.Document{}, fmt.Errorf("http fetch: parse final URL: %w", err)
	}

	contentType := detectContentType(response.Header.Get("content-type"), parsedFinalURL.Path)
	document := page.Document{
		Provider:   page.ProviderTLS,
		StatusCode: response.StatusCode,
		FinalURL:   finalURL,
		Type:       contentType,
		Session: page.Session{
			UserAgent: effectiveUserAgent,
			Cookies:   portableCookies(client.GetCookies(parsedFinalURL), parsedFinalURL.Hostname()),
		},
	}
	if contentType == page.ContentHTML {
		document.HTML = string(body)
	} else if contentType == page.ContentText {
		document.Text = strings.TrimSpace(string(body))
	}
	return document, nil
}

func requestHeaders(userAgent string, session page.Session) http.Header {
	if !session.Replayable() {
		return http.Header{
			"accept":                    {"text/html,application/xhtml+xml,application/xml;q=0.9,text/plain;q=0.8,*/*;q=0.7"},
			"accept-language":           {"en-GB,en;q=0.9"},
			"cache-control":             {"no-cache"},
			"pragma":                    {"no-cache"},
			"upgrade-insecure-requests": {"1"},
			"user-agent":                {userAgent},
			http.HeaderOrderKey:         {"accept", "accept-language", "cache-control", "pragma", "upgrade-insecure-requests", "user-agent"},
		}
	}
	browser := func(name, fallback string) []string {
		if value := session.Headers[name]; value != "" {
			return []string{value}
		}
		return []string{fallback}
	}
	return http.Header{
		"sec-ch-ua":                 browser("sec-ch-ua", ""),
		"sec-ch-ua-mobile":          browser("sec-ch-ua-mobile", "?0"),
		"sec-ch-ua-platform":        browser("sec-ch-ua-platform", ""),
		"upgrade-insecure-requests": {"1"},
		"user-agent":                {userAgent},
		"accept":                    browser("accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8"),
		"sec-fetch-site":            {"none"},
		"sec-fetch-mode":            {"navigate"},
		"sec-fetch-user":            {"?1"},
		"sec-fetch-dest":            {"document"},
		"accept-language":           browser("accept-language", "en-US,en;q=0.9"),
		"priority":                  {"u=0, i"},
		http.HeaderOrderKey: {
			"sec-ch-ua", "sec-ch-ua-mobile", "sec-ch-ua-platform", "upgrade-insecure-requests", "user-agent", "accept",
			"sec-fetch-site", "sec-fetch-mode", "sec-fetch-user", "sec-fetch-dest", "accept-encoding", "accept-language", "priority",
		},
	}
}

func requestCookies(cookies []page.Cookie) []*http.Cookie {
	result := make([]*http.Cookie, 0, len(cookies))
	for _, cookie := range cookies {
		current := &http.Cookie{
			Name:     cookie.Name,
			Value:    cookie.Value,
			Domain:   cookie.Domain,
			Path:     cookie.Path,
			HttpOnly: cookie.HTTPOnly,
			Secure:   cookie.Secure,
		}
		if cookie.Expires > 0 {
			current.Expires = time.Unix(int64(cookie.Expires), 0)
		}
		result = append(result, current)
	}
	return result
}

func detectContentType(header string, path string) page.ContentType {
	mediaType, _, _ := mime.ParseMediaType(header)
	switch {
	case mediaType == "text/html" || mediaType == "application/xhtml+xml" || mediaType == "":
		if strings.HasSuffix(strings.ToLower(path), ".pdf") {
			return page.ContentPDF
		}
		return page.ContentHTML
	case mediaType == "application/pdf":
		return page.ContentPDF
	case strings.HasPrefix(mediaType, "text/") || mediaType == "application/json" || mediaType == "application/xml":
		return page.ContentText
	default:
		return page.ContentUnknown
	}
}

func portableCookies(cookies []*http.Cookie, defaultDomain string) []page.Cookie {
	result := make([]page.Cookie, 0, len(cookies))
	for _, cookie := range cookies {
		domain := cookie.Domain
		if domain == "" {
			domain = defaultDomain
		}
		path := cookie.Path
		if path == "" {
			path = "/"
		}
		expires := float64(0)
		if !cookie.Expires.IsZero() {
			expires = float64(cookie.Expires.Unix())
		}
		result = append(result, page.Cookie{
			Name:     cookie.Name,
			Value:    cookie.Value,
			Domain:   domain,
			Path:     path,
			Expires:  expires,
			HTTPOnly: cookie.HttpOnly,
			Secure:   cookie.Secure,
			SameSite: sameSite(cookie.SameSite),
		})
	}
	return result
}

func sameSite(value http.SameSite) string {
	switch value {
	case http.SameSiteLaxMode:
		return "Lax"
	case http.SameSiteStrictMode:
		return "Strict"
	case http.SameSiteNoneMode:
		return "None"
	default:
		return ""
	}
}
