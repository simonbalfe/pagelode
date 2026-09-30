package capsolver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/simonbalfe/pagelode/internal/page"
)

const maxResponseBytes = 1 << 20

type Fetcher interface {
	Fetch(context.Context, string, page.Session) (page.Document, error)
}

type Client struct {
	apiKey           string
	apiURL           string
	proxy            string
	http             *http.Client
	challengeFetcher Fetcher
	solvedFetcher    Fetcher
	pollWait         time.Duration
}

type taskRequest struct {
	ClientKey string `json:"clientKey"`
	Task      task   `json:"task"`
}

type task struct {
	Type       string `json:"type"`
	WebsiteURL string `json:"websiteURL"`
	Proxy      string `json:"proxy"`
	UserAgent  string `json:"userAgent,omitempty"`
	HTML       string `json:"html,omitempty"`
}

type taskResultRequest struct {
	ClientKey string `json:"clientKey"`
	TaskID    string `json:"taskId"`
}

type response struct {
	ErrorID          int      `json:"errorId"`
	ErrorCode        string   `json:"errorCode"`
	ErrorDescription string   `json:"errorDescription"`
	TaskID           string   `json:"taskId"`
	Status           string   `json:"status"`
	Solution         solution `json:"solution"`
}

type solution struct {
	UserAgent string            `json:"userAgent"`
	Cookies   map[string]string `json:"cookies"`
}

func New(apiKey string, apiURL string, proxyURL string, challengeFetcher Fetcher, solvedFetcher Fetcher) (*Client, error) {
	if apiKey == "" || proxyURL == "" {
		return nil, nil
	}
	proxy, err := solverProxy(proxyURL)
	if err != nil {
		return nil, fmt.Errorf("capsolver: configure proxy: %w", err)
	}
	return &Client{
		apiKey:           apiKey,
		apiURL:           strings.TrimRight(apiURL, "/"),
		proxy:            proxy,
		http:             &http.Client{Timeout: 30 * time.Second},
		challengeFetcher: challengeFetcher,
		solvedFetcher:    solvedFetcher,
		pollWait:         2 * time.Second,
	}, nil
}

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

func (c *Client) Fetch(ctx context.Context, targetURL string, session page.Session) (page.Document, error) {
	bootstrap, err := c.challengeFetcher.Fetch(ctx, targetURL, session)
	if err != nil {
		return page.Document{}, fmt.Errorf("capsolver: fetch challenge page: %w", err)
	}
	debug("challenge_fetched", map[string]any{"status": bootstrap.StatusCode, "user_agent_major": chromeMajor(bootstrap.Session.UserAgent), "cookies": len(bootstrap.Session.Cookies)})
	created, err := c.call(ctx, "/createTask", taskRequest{
		ClientKey: c.apiKey,
		Task: task{
			Type:       "AntiCloudflareTask",
			WebsiteURL: targetURL,
			Proxy:      c.proxy,
			UserAgent:  bootstrap.Session.UserAgent,
			HTML:       bootstrap.HTML,
		},
	})
	if err != nil {
		return page.Document{}, err
	}
	if created.TaskID == "" {
		return page.Document{}, errors.New("capsolver: create task returned no task ID")
	}

	result, err := c.wait(ctx, created.TaskID)
	if err != nil {
		return page.Document{}, err
	}
	debug("challenge_solved", map[string]any{"user_agent_major": chromeMajor(result.Solution.UserAgent), "cookies": len(result.Solution.Cookies)})
	parsed, err := url.Parse(targetURL)
	if err != nil {
		return page.Document{}, fmt.Errorf("capsolver: parse target URL: %w", err)
	}
	solved := bootstrap.Session
	solved.UserAgent = result.Solution.UserAgent
	cookieIndexes := make(map[string]int, len(solved.Cookies))
	for index, cookie := range solved.Cookies {
		cookieIndexes[cookie.Name] = index
	}
	for name, value := range result.Solution.Cookies {
		cookie := page.Cookie{
			Name:   name,
			Value:  value,
			Domain: parsed.Hostname(),
			Path:   "/",
		}
		if index, ok := cookieIndexes[name]; ok {
			solved.Cookies[index] = cookie
		} else {
			solved.Cookies = append(solved.Cookies, cookie)
		}
	}
	document, err := c.solvedFetcher.Fetch(ctx, targetURL, solved)
	if err != nil {
		return page.Document{}, fmt.Errorf("capsolver: fetch solved page: %w", err)
	}
	document.Provider = page.ProviderCapSolver
	return document, nil
}

func debug(event string, values map[string]any) {
	if os.Getenv("PAGELODE_DEBUG") != "true" {
		return
	}
	values["component"] = "capsolver"
	values["event"] = event
	encoded, err := json.Marshal(values)
	if err == nil {
		_, _ = fmt.Fprintln(os.Stderr, string(encoded))
	}
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

func (c *Client) wait(ctx context.Context, taskID string) (response, error) {
	ticker := time.NewTicker(c.pollWait)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return response{}, fmt.Errorf("capsolver: wait for solution: %w", ctx.Err())
		case <-ticker.C:
			result, err := c.call(ctx, "/getTaskResult", taskResultRequest{ClientKey: c.apiKey, TaskID: taskID})
			if err != nil {
				return response{}, err
			}
			switch result.Status {
			case "ready":
				if result.Solution.UserAgent == "" || len(result.Solution.Cookies) == 0 {
					return response{}, errors.New("capsolver: solution omitted browser session")
				}
				return result, nil
			case "processing":
			default:
				return response{}, fmt.Errorf("capsolver: unexpected task status %q", result.Status)
			}
		}
	}
}

func (c *Client) call(ctx context.Context, path string, payload any) (response, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return response{}, fmt.Errorf("capsolver: encode request: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.apiURL+path, bytes.NewReader(body))
	if err != nil {
		return response{}, fmt.Errorf("capsolver: create request: %w", err)
	}
	request.Header.Set("content-type", "application/json")
	httpResponse, err := c.http.Do(request)
	if err != nil {
		return response{}, fmt.Errorf("capsolver: request: %w", err)
	}
	defer httpResponse.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(httpResponse.Body, maxResponseBytes+1))
	if err != nil {
		return response{}, fmt.Errorf("capsolver: read response: %w", err)
	}
	if len(responseBody) > maxResponseBytes {
		return response{}, errors.New("capsolver: response too large")
	}
	var result response
	if err := json.Unmarshal(responseBody, &result); err != nil {
		if httpResponse.StatusCode < 200 || httpResponse.StatusCode >= 300 {
			return response{}, fmt.Errorf("capsolver: API returned HTTP %d", httpResponse.StatusCode)
		}
		return response{}, fmt.Errorf("capsolver: decode response: %w", err)
	}
	if result.ErrorID != 0 {
		detail := result.ErrorCode
		if description := c.safeDescription(result.ErrorDescription); description != "" {
			detail += ": " + description
		}
		if detail == "" {
			detail = "unknown_error"
		}
		return response{}, fmt.Errorf("capsolver: API error: %s", detail)
	}
	if httpResponse.StatusCode < 200 || httpResponse.StatusCode >= 300 {
		return response{}, fmt.Errorf("capsolver: API returned HTTP %d", httpResponse.StatusCode)
	}
	return result, nil
}

func (c *Client) safeDescription(description string) string {
	description = strings.ReplaceAll(description, c.apiKey, "[redacted]")
	parts := strings.Split(c.proxy, ":")
	if len(parts) >= 5 {
		description = strings.ReplaceAll(description, parts[3], "[redacted]")
		description = strings.ReplaceAll(description, strings.Join(parts[4:], ":"), "[redacted]")
	}
	description = strings.TrimSpace(description)
	if len(description) > 300 {
		description = description[:300]
	}
	return description
}

func solverProxy(raw string) (string, error) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	if parsed.Hostname() == "" || parsed.Port() == "" {
		return "", errors.New("proxy must include host and port")
	}
	if parsed.User == nil {
		return "", errors.New("proxy must include username and password")
	}
	password, ok := parsed.User.Password()
	if !ok || parsed.User.Username() == "" || password == "" {
		return "", errors.New("proxy must include username and password")
	}
	return strings.Join([]string{parsed.Scheme, parsed.Hostname(), parsed.Port(), parsed.User.Username(), password}, ":"), nil
}
