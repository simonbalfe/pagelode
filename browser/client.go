// Package browser is the Go client for the Bun browser service in
// browser-service/, which renders pages, opens interactive sessions, and
// retires proxy identities over HTTP (/v1/render, /v1/sessions,
// /v1/identities). A 429 from the service is wrapped with limit.ErrSaturated
// so callers can back off with errors.Is.
package browser

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/simonbalfe/pagelode/limit"
	"github.com/simonbalfe/pagelode/page"
)

const (
	maxResponseBytes = 32 << 20
	defaultTimeout   = 45 * time.Second
	minimumTimeout   = time.Second
	rotationTimeout  = 10 * time.Second

	defaultExploreClicks = 10
	maxExploreClicks     = 50
)

// Client sends render and session requests to the browser service. It is
// safe for concurrent use; the optional Identities assigns rotating proxies.
type Client struct {
	baseURL    string
	token      string
	settle     time.Duration
	identities *Identities
	http       *http.Client
}

// SessionRequest describes an interactive browser session to open. A
// non-empty Profile pins the session to that profile's proxy; TTL is sent in
// whole seconds.
type SessionRequest struct {
	Profile       string
	CreateProfile bool
	Headless      *bool
	StartURL      string
	TTL           time.Duration
}

// Session is an open browser session that callers drive over CDPURL until
// ExpiresAt or CloseSession.
type Session struct {
	ID        string
	CDPURL    string
	Profile   string
	Headless  bool
	ExpiresAt time.Time
}

// New returns a Client for the service at baseURL, which must be an absolute
// http or https URL. token is sent as a bearer token when non-empty, settle
// is how long renders wait after load, and identities may be nil to disable
// proxying.
func New(baseURL, token string, settle time.Duration, identities *Identities) (*Client, error) {
	parsed, err := url.Parse(baseURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return nil, fmt.Errorf("browser service URL %q must be an absolute http or https URL", baseURL)
	}
	return &Client{baseURL: strings.TrimRight(baseURL, "/"), token: token, settle: settle, identities: identities, http: &http.Client{}}, nil
}

// Fetch renders targetURL with the session's cookies and returns the
// resulting document. Blocked responses count towards identity rotation.
func (c *Client) Fetch(ctx context.Context, targetURL string, session page.Session) (page.Document, error) {
	return c.render(ctx, targetURL, session, nil, nil)
}

// Capture renders targetURL while recording network traffic per options,
// using the profile's proxy when options.Profile is set.
func (c *Client) Capture(ctx context.Context, targetURL string, session page.Session, options page.CaptureOptions) (page.Document, error) {
	return c.render(ctx, targetURL, session, &options, nil)
}

// Explore renders targetURL and lets the service click up to
// options.MaxClicks interactive elements (default 10, capped at 50).
func (c *Client) Explore(ctx context.Context, targetURL string, session page.Session, options page.ExploreOptions) (page.Document, error) {
	return c.render(ctx, targetURL, session, nil, &exploreRequest{MaxClicks: clampClicks(options.MaxClicks)})
}

func clampClicks(requested int) int {
	if requested == 0 {
		requested = defaultExploreClicks
	}
	return max(0, min(requested, maxExploreClicks))
}

// CreateSession opens an interactive browser session via POST /v1/sessions.
func (c *Client) CreateSession(ctx context.Context, request SessionRequest) (Session, error) {
	proxyURL, err := c.sessionProxy(request.Profile)
	if err != nil {
		return Session{}, err
	}
	body := sessionRequest{
		ProxyURL:      proxyURL,
		Profile:       request.Profile,
		CreateProfile: request.CreateProfile,
		Headless:      request.Headless,
		StartURL:      request.StartURL,
		TTLSeconds:    int(request.TTL / time.Second),
	}
	var response sessionResponse
	if err := c.do(ctx, http.MethodPost, "/v1/sessions", body, &response); err != nil {
		return Session{}, err
	}
	return Session{ID: response.ID, CDPURL: response.CDPURL, Profile: response.Profile, Headless: response.Headless, ExpiresAt: response.ExpiresAt}, nil
}

// CloseSession closes the session with the given id.
func (c *Client) CloseSession(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, "/v1/sessions/"+url.PathEscape(id), nil, nil)
}

func (c *Client) render(ctx context.Context, targetURL string, session page.Session, capture *page.CaptureOptions, explore *exploreRequest) (page.Document, error) {
	timeout := defaultTimeout
	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline)
		if remaining < minimumTimeout {
			return page.Document{}, context.DeadlineExceeded
		}
		timeout = min(timeout, remaining)
	}
	request := renderRequest{
		URL:             targetURL,
		TimeoutMS:       timeout.Milliseconds(),
		WaitUntil:       "domcontentloaded",
		SettleMS:        c.settle.Milliseconds(),
		Cookies:         session.Cookies,
		SettleChallenge: true,
		Explore:         explore,
	}
	if capture != nil {
		request.Capture = &captureRequest{WaitMS: capture.WaitMS, MaxRequests: page.MaxCaptureRequests, MaxBodyBytes: page.MaxCaptureBodyBytes, MaxTotalBytes: page.MaxCaptureTotalBytes}
		request.Profile = capture.Profile
	}
	if c.identities != nil {
		if request.Profile != "" {
			request.ProxyURL = c.identities.profileProxy(request.Profile)
		} else {
			request.Identity, request.ProxyURL = c.identities.current()
		}
	}
	var response renderResponse
	if err := c.do(ctx, http.MethodPost, "/v1/render", request, &response); err != nil {
		return page.Document{}, err
	}
	document := document(response, session)
	if request.Identity != "" {
		c.observe(ctx, request.Identity, targetURL, document)
	}
	return document, nil
}

func (c *Client) observe(ctx context.Context, key string, targetURL string, document page.Document) {
	target, err := url.Parse(targetURL)
	if err != nil {
		return
	}
	blocked := document.Challenge != nil || document.StatusCode == http.StatusUnauthorized || document.StatusCode == http.StatusForbidden || document.StatusCode == http.StatusTooManyRequests
	retired, rotated, err := c.identities.observe(key, strings.ToLower(target.Hostname()), blocked)
	if err != nil {
		slog.Warn("identity generation not persisted", "error", err)
	}
	if !rotated {
		return
	}
	rotateCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), rotationTimeout)
	defer cancel()
	if err := c.do(rotateCtx, http.MethodDelete, "/v1/identities/"+url.PathEscape(retired), nil, nil); err != nil {
		slog.Warn("identity rotation not applied by browser service", "identity", retired, "error", err)
	}
}

func (c *Client) sessionProxy(profile string) (string, error) {
	if c.identities == nil {
		return "", nil
	}
	if profile != "" {
		return c.identities.profileProxy(profile), nil
	}
	seed := make([]byte, 8)
	if _, err := rand.Read(seed); err != nil {
		return "", fmt.Errorf("patchright: choose session proxy: %w", err)
	}
	return c.identities.proxyFor(fmt.Sprintf("session:%x", seed)), nil
}

func (c *Client) do(ctx context.Context, method, path string, body, result any) error {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("patchright: encode request: %w", err)
		}
		reader = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return fmt.Errorf("patchright: build request: %w", err)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		request.Header.Set("Authorization", "Bearer "+c.token)
	}
	response, err := c.http.Do(request)
	if err != nil {
		return fmt.Errorf("patchright: %w", err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return fmt.Errorf("patchright: read response: %w", err)
	}
	if len(data) > maxResponseBytes {
		return errors.New("patchright: response exceeds size limit")
	}
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return serviceError(response.StatusCode, data)
	}
	if result == nil {
		return nil
	}
	if err := json.Unmarshal(data, result); err != nil {
		return fmt.Errorf("patchright: decode response: %w", err)
	}
	return nil
}

func serviceError(status int, data []byte) error {
	var envelope errorEnvelope
	if err := json.Unmarshal(data, &envelope); err != nil || envelope.Error.Code == "" {
		envelope.Error = errorBody{Code: "http_" + fmt.Sprint(status), Message: http.StatusText(status)}
	}
	err := fmt.Errorf("patchright: %s: %s", envelope.Error.Code, envelope.Error.Message)
	if status == http.StatusTooManyRequests {
		return fmt.Errorf("%w: %w", err, limit.ErrSaturated)
	}
	return err
}

func document(response renderResponse, session page.Session) page.Document {
	if response.UserAgent != "" {
		session.UserAgent = response.UserAgent
	}
	session.Cookies = response.Cookies
	session.ProxyURL = response.Replay.ProxyURL
	session.Headers = response.Replay.Headers
	return page.Document{
		Provider:     page.ProviderPatchright,
		StatusCode:   response.StatusCode,
		FinalURL:     response.FinalURL,
		Title:        response.Title,
		HTML:         response.HTML,
		Type:         page.ContentHTML,
		Headers:      response.Headers,
		Challenge:    response.Challenge,
		Session:      session,
		Traffic:      response.Traffic,
		ExploredURLs: response.ExploredURLs,
	}
}

type renderRequest struct {
	URL             string          `json:"url"`
	TimeoutMS       int64           `json:"timeout_ms"`
	WaitUntil       string          `json:"wait_until"`
	SettleMS        int64           `json:"settle_ms"`
	Cookies         []page.Cookie   `json:"cookies,omitempty"`
	ProxyURL        string          `json:"proxy_url,omitempty"`
	SettleChallenge bool            `json:"settle_challenge"`
	Capture         *captureRequest `json:"capture,omitempty"`
	Explore         *exploreRequest `json:"explore,omitempty"`
	Profile         string          `json:"profile,omitempty"`
	Identity        string          `json:"identity,omitempty"`
}

type captureRequest struct {
	WaitMS        int `json:"wait_ms"`
	MaxRequests   int `json:"max_requests"`
	MaxBodyBytes  int `json:"max_body_bytes"`
	MaxTotalBytes int `json:"max_total_bytes"`
}

type exploreRequest struct {
	MaxClicks int `json:"max_clicks"`
}

type renderResponse struct {
	StatusCode   int               `json:"status_code"`
	FinalURL     string            `json:"final_url"`
	Title        string            `json:"title"`
	HTML         string            `json:"html"`
	UserAgent    string            `json:"user_agent"`
	Headers      map[string]string `json:"headers"`
	Challenge    *page.Challenge   `json:"challenge,omitempty"`
	Cookies      []page.Cookie     `json:"cookies"`
	Traffic      *page.Capture     `json:"traffic,omitempty"`
	ExploredURLs []string          `json:"explored_urls,omitempty"`
	Replay       replay            `json:"replay"`
}

type replay struct {
	ProxyURL string            `json:"proxy_url"`
	Headers  map[string]string `json:"headers"`
}

type sessionRequest struct {
	ProxyURL      string `json:"proxy_url,omitempty"`
	Profile       string `json:"profile,omitempty"`
	CreateProfile bool   `json:"create_profile,omitempty"`
	Headless      *bool  `json:"headless,omitempty"`
	StartURL      string `json:"start_url,omitempty"`
	TTLSeconds    int    `json:"ttl_seconds,omitempty"`
}

type sessionResponse struct {
	ID        string    `json:"id"`
	CDPURL    string    `json:"cdp_url"`
	Profile   string    `json:"profile"`
	Headless  bool      `json:"headless"`
	ExpiresAt time.Time `json:"expires_at"`
}

type errorEnvelope struct {
	Error errorBody `json:"error"`
}

type errorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
