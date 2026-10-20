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
	"sync"
	"sync/atomic"
	"time"

	"github.com/simonbalfe/pagelode/limit"
	"github.com/simonbalfe/pagelode/page"
)

const (
	maxResponseBytes = 32 << 20
	defaultTimeout   = 45 * time.Second
	minimumTimeout   = time.Second
	rotationTimeout  = 10 * time.Second
	unhealthyFor     = 15 * time.Second

	maxPendingRetirements = 64

	defaultExploreClicks = 10
	maxExploreClicks     = 50
)

type Client struct {
	nodes      []*node
	token      string
	settle     time.Duration
	identities *Identities
	http       *http.Client

	nextSession atomic.Uint64

	mu       sync.Mutex
	sessions map[string]openSession
	pending  []retirement
}

type node struct {
	baseURL   string
	slots     chan struct{}
	downUntil atomic.Int64
}

type openSession struct {
	node      *node
	expiresAt time.Time
}

type retirement struct {
	node *node
	key  string
}

type transportError struct {
	err error
}

func (e *transportError) Error() string { return e.err.Error() }

func (e *transportError) Unwrap() error { return e.err }

type SessionRequest struct {
	Profile       string
	CreateProfile bool
	Headless      *bool
	StartURL      string
	TTL           time.Duration
}

type Session struct {
	ID        string
	CDPURL    string
	Profile   string
	Headless  bool
	ExpiresAt time.Time
}

func New(baseURLs []string, token string, settle time.Duration, nodeSlots int, identities *Identities) (*Client, error) {
	if len(baseURLs) == 0 {
		return nil, errors.New("browser service: at least one URL is required")
	}
	if nodeSlots < 1 {
		return nil, fmt.Errorf("browser service: node slots must be at least 1, got %d", nodeSlots)
	}
	nodes := make([]*node, 0, len(baseURLs))
	for _, baseURL := range baseURLs {
		parsed, err := url.Parse(baseURL)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			return nil, fmt.Errorf("browser service URL %q must be an absolute http or https URL", baseURL)
		}
		nodes = append(nodes, &node{baseURL: strings.TrimRight(baseURL, "/"), slots: make(chan struct{}, nodeSlots)})
	}
	return &Client{
		nodes:      nodes,
		token:      token,
		settle:     settle,
		identities: identities,
		http:       &http.Client{},
		sessions:   make(map[string]openSession),
	}, nil
}

func (c *Client) Fetch(ctx context.Context, targetURL string, session page.Session) (page.Document, error) {
	return c.render(ctx, targetURL, session, nil, nil)
}

func (c *Client) Capture(ctx context.Context, targetURL string, session page.Session, options page.CaptureOptions) (page.Document, error) {
	return c.render(ctx, targetURL, session, &options, nil)
}

func (c *Client) Explore(ctx context.Context, targetURL string, session page.Session, options page.ExploreOptions) (page.Document, error) {
	return c.render(ctx, targetURL, session, nil, &exploreRequest{MaxClicks: clampClicks(options.MaxClicks)})
}

func clampClicks(requested int) int {
	if requested == 0 {
		requested = defaultExploreClicks
	}
	return max(0, min(requested, maxExploreClicks))
}

func (c *Client) CreateSession(ctx context.Context, request SessionRequest) (Session, error) {
	proxyURL, err := c.sessionProxy(request.Profile)
	if err != nil {
		return Session{}, err
	}
	preferred := int((c.nextSession.Add(1) - 1) % uint64(len(c.nodes)))
	if request.Profile != "" {
		preferred = bucket(request.Profile, len(c.nodes))
	}
	target, ok := c.healthyFrom(preferred)
	if !ok {
		return Session{}, fmt.Errorf("patchright: every browser service node is unavailable: %w", limit.ErrUnavailable)
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
	if err := c.do(ctx, c.nodes[target], http.MethodPost, "/v1/sessions", body, &response); err != nil {
		return Session{}, err
	}
	c.mu.Lock()
	now := time.Now()
	for id, open := range c.sessions {
		if !open.expiresAt.IsZero() && open.expiresAt.Before(now) {
			delete(c.sessions, id)
		}
	}
	c.sessions[response.ID] = openSession{node: c.nodes[target], expiresAt: response.ExpiresAt}
	c.mu.Unlock()
	return Session{ID: response.ID, CDPURL: response.CDPURL, Profile: response.Profile, Headless: response.Headless, ExpiresAt: response.ExpiresAt}, nil
}

func (c *Client) CloseSession(ctx context.Context, id string) error {
	c.mu.Lock()
	open, known := c.sessions[id]
	c.mu.Unlock()
	target := c.nodes[0]
	if known {
		target = open.node
	}
	err := c.do(ctx, target, http.MethodDelete, "/v1/sessions/"+url.PathEscape(id), nil, nil)
	var transport *transportError
	if known && !errors.As(err, &transport) {
		c.mu.Lock()
		delete(c.sessions, id)
		c.mu.Unlock()
	}
	return err
}

func (c *Client) render(ctx context.Context, targetURL string, session page.Session, capture *page.CaptureOptions, explore *exploreRequest) (page.Document, error) {
	if _, err := renderTimeout(ctx); err != nil {
		return page.Document{}, err
	}
	request := renderRequest{
		URL:             targetURL,
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
	host := hostname(targetURL)
	preferred := 0
	switch {
	case request.Profile != "":
		preferred = bucket(request.Profile, len(c.nodes))
		if c.identities != nil {
			request.ProxyURL = c.identities.profileProxy(request.Profile)
		}
	case c.identities != nil:
		var slot int
		slot, request.Identity, request.ProxyURL = c.identities.identity(host)
		preferred = slot % len(c.nodes)
	}
	var response renderResponse
	served, err := c.renderAnywhere(ctx, preferred, request, &response)
	if err != nil {
		return page.Document{}, err
	}
	document := document(response, session)
	if request.Identity != "" {
		c.observe(ctx, served, request.Identity, host, document)
	}
	return document, nil
}

func (c *Client) renderAnywhere(ctx context.Context, preferred int, request renderRequest, response *renderResponse) (*node, error) {
	first, ok := c.healthyFrom(preferred)
	if !ok {
		return nil, fmt.Errorf("patchright: every browser service node is unavailable: %w", limit.ErrUnavailable)
	}
	err := c.renderOn(ctx, c.nodes[first], request, response)
	var transport *transportError
	if !errors.As(err, &transport) {
		return c.nodes[first], err
	}
	second, ok := c.healthyFrom(first + 1)
	if !ok {
		return nil, fmt.Errorf("%w: %w", err, limit.ErrUnavailable)
	}
	err = c.renderOn(ctx, c.nodes[second], request, response)
	if errors.As(err, &transport) {
		return nil, fmt.Errorf("%w: %w", err, limit.ErrUnavailable)
	}
	return c.nodes[second], err
}

func (c *Client) renderOn(ctx context.Context, target *node, request renderRequest, response *renderResponse) error {
	select {
	case target.slots <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-target.slots }()
	timeout, err := renderTimeout(ctx)
	if err != nil {
		return err
	}
	request.TimeoutMS = timeout.Milliseconds()
	return c.do(ctx, target, http.MethodPost, "/v1/render", request, response)
}

func renderTimeout(ctx context.Context) (time.Duration, error) {
	deadline, ok := ctx.Deadline()
	if !ok {
		return defaultTimeout, nil
	}
	remaining := time.Until(deadline)
	if remaining < minimumTimeout {
		return 0, context.DeadlineExceeded
	}
	return min(defaultTimeout, remaining), nil
}

func (c *Client) healthyFrom(start int) (int, bool) {
	now := time.Now().UnixNano()
	for offset := range len(c.nodes) {
		index := (start + offset) % len(c.nodes)
		if c.nodes[index].downUntil.Load() <= now {
			return index, true
		}
	}
	return 0, false
}

func (c *Client) observe(ctx context.Context, served *node, key string, host string, document page.Document) {
	if host == "" {
		return
	}
	blocked := document.Challenge != nil || document.StatusCode == http.StatusUnauthorized || document.StatusCode == http.StatusForbidden || document.StatusCode == http.StatusTooManyRequests
	retired, rotated, err := c.identities.observe(key, host, blocked)
	if err != nil {
		slog.Warn("identity generation not persisted", "error", err)
	}
	if !rotated {
		return
	}
	c.mu.Lock()
	c.pending = append(c.pending, retirement{node: served, key: retired})
	batch := c.pending
	c.pending = nil
	c.mu.Unlock()
	go c.retire(context.WithoutCancel(ctx), batch)
}

func (c *Client) retire(ctx context.Context, batch []retirement) {
	ctx, cancel := context.WithTimeout(ctx, rotationTimeout)
	defer cancel()
	var failed []retirement
	for _, current := range batch {
		if err := c.do(ctx, current.node, http.MethodDelete, "/v1/identities/"+url.PathEscape(current.key), nil, nil); err != nil {
			slog.Warn("identity rotation not applied by browser service", "identity", current.key, "node", current.node.baseURL, "error", err)
			failed = append(failed, current)
		}
	}
	if len(failed) == 0 {
		return
	}
	c.mu.Lock()
	c.pending = append(failed, c.pending...)
	if excess := len(c.pending) - maxPendingRetirements; excess > 0 {
		c.pending = c.pending[excess:]
	}
	c.mu.Unlock()
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
	return c.identities.seededProxy(fmt.Sprintf("session:%x", seed)), nil
}

func (c *Client) do(ctx context.Context, target *node, method, path string, body, result any) error {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("patchright: encode request: %w", err)
		}
		reader = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, target.baseURL+path, reader)
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
		if ctx.Err() != nil {
			return fmt.Errorf("patchright: %w", err)
		}
		target.downUntil.Store(time.Now().Add(unhealthyFor).UnixNano())
		return &transportError{err: fmt.Errorf("patchright: %w", err)}
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

func hostname(targetURL string) string {
	target, err := url.Parse(targetURL)
	if err != nil {
		return ""
	}
	return strings.ToLower(target.Hostname())
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
