package browser

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/simonbalfe/pagelode/internal/limit"
	"github.com/simonbalfe/pagelode/internal/page"
)

const (
	maxResponseBytes = 32 << 20
	defaultTimeout   = 45 * time.Second
	minimumTimeout   = time.Second
)

type Client struct {
	baseURL  string
	token    string
	proxyURL string
	http     *http.Client
}

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

func New(baseURL, token, proxyURL string) (*Client, error) {
	parsed, err := url.Parse(baseURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return nil, fmt.Errorf("browser service URL %q must be an absolute http or https URL", baseURL)
	}
	return &Client{baseURL: strings.TrimRight(baseURL, "/"), token: token, proxyURL: proxyURL, http: &http.Client{}}, nil
}

func (c *Client) Fetch(ctx context.Context, targetURL string, session page.Session) (page.Document, error) {
	return c.render(ctx, targetURL, session, nil)
}

func (c *Client) Capture(ctx context.Context, targetURL string, session page.Session, options page.CaptureOptions) (page.Document, error) {
	return c.render(ctx, targetURL, session, &options)
}

func (c *Client) CreateSession(ctx context.Context, request SessionRequest) (Session, error) {
	body := sessionRequest{
		ProxyURL:      c.proxyURL,
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

func (c *Client) CloseSession(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, "/v1/sessions/"+url.PathEscape(id), nil, nil)
}

func (c *Client) render(ctx context.Context, targetURL string, session page.Session, capture *page.CaptureOptions) (page.Document, error) {
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
		Cookies:         session.Cookies,
		ProxyURL:        c.proxyURL,
		SettleChallenge: true,
	}
	if capture != nil {
		request.Capture = &captureRequest{WaitMS: capture.WaitMS}
		request.Profile = capture.Profile
	}
	var response renderResponse
	if err := c.do(ctx, http.MethodPost, "/v1/render", request, &response); err != nil {
		return page.Document{}, err
	}
	return document(response, session), nil
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
	return page.Document{
		Provider:   page.ProviderPatchright,
		StatusCode: response.StatusCode,
		FinalURL:   response.FinalURL,
		Title:      response.Title,
		HTML:       response.HTML,
		Type:       page.ContentHTML,
		Session:    session,
		Traffic:    response.Traffic,
	}
}

type renderRequest struct {
	URL             string          `json:"url"`
	TimeoutMS       int64           `json:"timeout_ms"`
	WaitUntil       string          `json:"wait_until"`
	Cookies         []page.Cookie   `json:"cookies,omitempty"`
	ProxyURL        string          `json:"proxy_url,omitempty"`
	SettleChallenge bool            `json:"settle_challenge"`
	Capture         *captureRequest `json:"capture,omitempty"`
	Profile         string          `json:"profile,omitempty"`
}

type captureRequest struct {
	WaitMS int `json:"wait_ms"`
}

type renderResponse struct {
	StatusCode int           `json:"status_code"`
	FinalURL   string        `json:"final_url"`
	Title      string        `json:"title"`
	HTML       string        `json:"html"`
	UserAgent  string        `json:"user_agent"`
	Cookies    []page.Cookie `json:"cookies"`
	Traffic    *page.Capture `json:"traffic,omitempty"`
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
