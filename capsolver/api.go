package capsolver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const maxResponseBytes = 1 << 20

// API is a CapSolver task API client that creates tasks and polls for their
// results. Error descriptions redact the API key and proxy credentials.
type API struct {
	key      string
	url      string
	http     *http.Client
	pollWait time.Duration
}

// RecaptchaTask describes a reCAPTCHA v2 challenge; ProxyURL is optional.
type RecaptchaTask struct {
	WebsiteURL string
	WebsiteKey string
	DataS      string
	ProxyURL   string
}

// DataDomeTask describes a DataDome slider challenge; ProxyURL is required.
type DataDomeTask struct {
	CaptchaURL string
	UserAgent  string
	ProxyURL   string
}

type task struct {
	Type       string `json:"type"`
	WebsiteURL string `json:"websiteURL,omitempty"`
	WebsiteKey string `json:"websiteKey,omitempty"`
	DataS      string `json:"recaptchaDataSValue,omitempty"`
	CaptchaURL string `json:"captchaUrl,omitempty"`
	Proxy      string `json:"proxy,omitempty"`
	UserAgent  string `json:"userAgent,omitempty"`
	HTML       string `json:"html,omitempty"`
}

type taskRequest struct {
	ClientKey string `json:"clientKey"`
	Task      task   `json:"task"`
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
	UserAgent          string            `json:"userAgent"`
	Cookies            map[string]string `json:"cookies"`
	Cookie             string            `json:"cookie"`
	GRecaptchaResponse string            `json:"gRecaptchaResponse"`
}

// NewAPI returns an API for apiURL, or nil when apiKey is empty.
func NewAPI(apiKey string, apiURL string) *API {
	if apiKey == "" {
		return nil
	}
	return &API{
		key:      apiKey,
		url:      strings.TrimRight(apiURL, "/"),
		http:     &http.Client{Timeout: 30 * time.Second},
		pollWait: 2 * time.Second,
	}
}

// Recaptcha solves request and returns the g-recaptcha-response token. It
// uses the proxyless task type when ProxyURL is empty.
func (a *API) Recaptcha(ctx context.Context, request RecaptchaTask) (string, error) {
	solverTask := task{Type: "ReCaptchaV2TaskProxyLess", WebsiteURL: request.WebsiteURL, WebsiteKey: request.WebsiteKey, DataS: request.DataS}
	if request.ProxyURL != "" {
		proxy, err := taskProxy(request.ProxyURL)
		if err != nil {
			return "", fmt.Errorf("capsolver: reCAPTCHA proxy: %w", err)
		}
		solverTask.Type = "ReCaptchaV2Task"
		solverTask.Proxy = proxy
	}
	solved, err := a.solve(ctx, solverTask)
	if err != nil {
		return "", err
	}
	if solved.GRecaptchaResponse == "" {
		return "", errors.New("capsolver: solution omitted reCAPTCHA token")
	}
	return solved.GRecaptchaResponse, nil
}

// DataDome solves request and returns the DataDome cookie to replay.
func (a *API) DataDome(ctx context.Context, request DataDomeTask) (string, error) {
	proxy, err := taskProxy(request.ProxyURL)
	if err != nil {
		return "", fmt.Errorf("capsolver: DataDome proxy: %w", err)
	}
	solved, err := a.solve(ctx, task{Type: "DatadomeSliderTask", CaptchaURL: request.CaptchaURL, UserAgent: request.UserAgent, Proxy: proxy})
	if err != nil {
		return "", err
	}
	if solved.Cookie == "" {
		return "", errors.New("capsolver: solution omitted DataDome cookie")
	}
	return solved.Cookie, nil
}

func (a *API) solve(ctx context.Context, solverTask task) (solution, error) {
	started := time.Now()
	created, err := a.call(ctx, "/createTask", taskRequest{ClientKey: a.key, Task: solverTask}, solverTask.Proxy)
	if err != nil {
		return solution{}, err
	}
	if created.TaskID == "" {
		return solution{}, errors.New("capsolver: create task returned no task ID")
	}
	result, err := a.wait(ctx, created.TaskID, solverTask.Proxy)
	if err != nil {
		return solution{}, err
	}
	debug("task_solved", map[string]any{"type": solverTask.Type, "seconds": time.Since(started).Round(time.Second).Seconds()})
	return result.Solution, nil
}

func (a *API) wait(ctx context.Context, taskID string, proxy string) (response, error) {
	ticker := time.NewTicker(a.pollWait)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return response{}, fmt.Errorf("capsolver: wait for solution: %w", ctx.Err())
		case <-ticker.C:
			result, err := a.call(ctx, "/getTaskResult", taskResultRequest{ClientKey: a.key, TaskID: taskID}, proxy)
			if err != nil {
				return response{}, err
			}
			switch result.Status {
			case "ready":
				return result, nil
			case "processing":
			default:
				return response{}, fmt.Errorf("capsolver: unexpected task status %q", result.Status)
			}
		}
	}
}

func (a *API) call(ctx context.Context, path string, payload any, proxy string) (response, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return response{}, fmt.Errorf("capsolver: encode request: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, a.url+path, bytes.NewReader(body))
	if err != nil {
		return response{}, fmt.Errorf("capsolver: create request: %w", err)
	}
	request.Header.Set("content-type", "application/json")
	httpResponse, err := a.http.Do(request)
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
		if description := a.redact(result.ErrorDescription, proxy); description != "" {
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

func (a *API) redact(description string, proxy string) string {
	description = strings.ReplaceAll(description, a.key, "[redacted]")
	parts := strings.Split(proxy, ":")
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

func taskProxy(raw string) (string, error) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	if parsed.Hostname() == "" || parsed.Port() == "" {
		return "", errors.New("proxy must include host and port")
	}
	parts := []string{parsed.Scheme, parsed.Hostname(), parsed.Port()}
	if parsed.User != nil && parsed.User.Username() != "" {
		password, _ := parsed.User.Password()
		parts = append(parts, parsed.User.Username(), password)
	}
	return strings.Join(parts, ":"), nil
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
