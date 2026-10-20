package discovery

import (
	"context"
	"errors"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/simonbalfe/pagelode/pkg/classify"
	"github.com/simonbalfe/pagelode/pkg/limit"
	"github.com/simonbalfe/pagelode/pkg/orchestrator"
	"github.com/simonbalfe/pagelode/pkg/page"
	"github.com/simonbalfe/pagelode/pkg/profile"
)

type Capturer interface {
	Capture(context.Context, string, page.Session, page.CaptureOptions) (page.Document, error)
}

type Service struct {
	browser         Capturer
	proxyConfigured bool
}

var ErrStealthProxy = errors.New("stealth discovery requires PAGELODE_PROXY_URL")

func New(browser Capturer) *Service {
	return &Service{browser: browser}
}

type Request struct {
	URL     string `json:"url"`
	WaitMS  int    `json:"waitMs,omitempty"`
	HAR     *HAR   `json:"har,omitempty"`
	Profile string `json:"profile,omitempty"`
	Stealth bool   `json:"stealth,omitempty"`
	Verbose bool   `json:"verbose,omitempty"`
}

func (r Request) Validate() (Request, error) {
	if r.Profile != "" {
		if err := profile.Validate(r.Profile); err != nil {
			return Request{}, err
		}
		if r.HAR != nil {
			return Request{}, errors.New("profile applies only to browser discovery")
		}
	}
	if r.HAR != nil {
		if len(r.HAR.Log.Entries) == 0 {
			return Request{}, errors.New("HAR must contain at least one entry")
		}
		if r.WaitMS != 0 {
			return Request{}, errors.New("waitMs applies only to browser discovery")
		}
		if r.Stealth {
			return Request{}, errors.New("stealth applies only to browser discovery")
		}
		return r, nil
	}
	if !strings.Contains(r.URL, "://") {
		r.URL = "https://" + r.URL
	}
	parsed, err := url.ParseRequestURI(r.URL)
	if err != nil || parsed.Hostname() == "" || parsed.User != nil || parsed.Scheme != "http" && parsed.Scheme != "https" {
		return Request{}, errors.New("discovery URL must be an HTTP(S) URL without credentials")
	}
	if r.WaitMS == 0 {
		r.WaitMS = 1500
	}
	if r.WaitMS < 100 || r.WaitMS > 10000 {
		return Request{}, errors.New("waitMs must be between 100 and 10000")
	}
	return r, nil
}

func (s *Service) Discover(ctx context.Context, request Request) (Report, error) {
	request, err := request.Validate()
	if err != nil {
		return Report{}, err
	}
	if request.HAR != nil {
		return reportView(AnalyzeHAR(*request.HAR, request.URL), request.Verbose), nil
	}
	if request.Stealth && !s.proxyConfigured {
		return Report{}, ErrStealthProxy
	}
	if s.browser == nil {
		return Report{}, errors.New("discovery failed: no browser capture available")
	}
	started := time.Now()
	document, err := s.browser.Capture(ctx, request.URL, page.Session{}, page.CaptureOptions{WaitMS: request.WaitMS, Profile: request.Profile})
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, limit.ErrSaturated) {
			return Report{}, err
		}
		return Report{}, errors.New("discovery failed: " + string(page.ProviderPatchright) + ": " + captureError(err))
	}
	classification := classify.Document(document)
	outcome := string(classification.Kind)
	if classification.Kind == classify.Accept {
		outcome = "ok"
	}
	attempt := orchestrator.Attempt{Provider: page.ProviderPatchright, Outcome: outcome, Status: document.StatusCode, DurationMS: time.Since(started).Milliseconds(), Classification: string(classification.Kind), Detail: classification.Reason}
	blocked := classification.Kind == classify.Blocked || classification.Kind == classify.Retryable
	if blocked && document.Traffic == nil {
		return Report{}, errors.New("discovery failed: " + string(page.ProviderPatchright) + ": " + classification.Reason)
	}
	report := Analyze(request.URL, document)
	report.Attempts = []orchestrator.Attempt{attempt}
	switch {
	case classification.Kind == classify.Dead:
		report.Outcome = "dead"
	case blocked:
		report.Outcome = "blocked"
		report.Warnings = append(report.Warnings, "page remained blocked in the browser")
	}
	return reportView(report, request.Verbose), nil
}

var errorURL = regexp.MustCompile(`https?://[^\s"']+`)

func captureError(err error) string {
	message := strings.SplitN(err.Error(), "\n", 2)[0]
	message = errorURL.ReplaceAllStringFunc(message, sanitizeURL)
	runes := []rune(message)
	if len(runes) > 240 {
		message = string(runes[:240])
	}
	return message
}

func (s *Service) WithProxy(configured bool) *Service {
	s.proxyConfigured = configured
	return s
}
