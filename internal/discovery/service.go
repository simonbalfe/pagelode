package discovery

import (
	"context"
	"errors"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/simonbalfe/pagelode/internal/classify"
	"github.com/simonbalfe/pagelode/internal/limit"
	"github.com/simonbalfe/pagelode/internal/memory"
	"github.com/simonbalfe/pagelode/internal/orchestrator"
	"github.com/simonbalfe/pagelode/internal/page"
	"github.com/simonbalfe/pagelode/internal/profile"
)

type Capturer interface {
	Capture(context.Context, string, page.Session, page.CaptureOptions) (page.Document, error)
}

type Service struct {
	chrome            Capturer
	patchright        Capturer
	routes            *memory.Routes
	limiter           *limit.Limiter
	chromeEnabled     bool
	profilesDirectory string
}

func New(chrome, patchright Capturer, routes *memory.Routes, limiter *limit.Limiter, chromeEnabled bool) *Service {
	return &Service{chrome: chrome, patchright: patchright, routes: routes, limiter: limiter, chromeEnabled: chromeEnabled}
}

type Request struct {
	URL     string `json:"url"`
	WaitMS  int    `json:"waitMs,omitempty"`
	HAR     *HAR   `json:"har,omitempty"`
	Profile string `json:"profile,omitempty"`
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
	parsed, err := url.Parse(request.URL)
	if err != nil {
		return Report{}, err
	}
	options := page.CaptureOptions{WaitMS: request.WaitMS}
	if request.Profile != "" {
		options.ProfileDirectory, err = profile.Directory(s.profilesDirectory, request.Profile, false)
		if err != nil {
			return Report{}, err
		}
	}
	preferred, _ := s.routes.Preferred(parsed.Hostname())
	providers := []page.Provider{page.ProviderChromedp, page.ProviderPatchright}
	if request.Profile != "" || preferred == page.ProviderPatchright || !s.chromeEnabled {
		providers = []page.Provider{page.ProviderPatchright}
	}
	attempts := []orchestrator.Attempt{}
	var last page.Document
	var session page.Session
	for _, provider := range providers {
		capturer := s.chrome
		if provider == page.ProviderPatchright {
			capturer = s.patchright
		}
		if capturer == nil {
			continue
		}
		started := time.Now()
		var document page.Document
		err := s.limiter.Run(ctx, func() error {
			var err error
			document, err = capturer.Capture(ctx, request.URL, session, options)
			return err
		})
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, limit.ErrSaturated) {
				return Report{}, err
			}
			attempts = append(attempts, orchestrator.Attempt{Provider: provider, Outcome: "error", DurationMS: time.Since(started).Milliseconds(), Detail: captureError(err)})
			continue
		}
		last = document
		session = document.Session
		classification := classify.Document(document)
		outcome := string(classification.Kind)
		if classification.Kind == classify.Accept {
			outcome = "ok"
		}
		attempts = append(attempts, orchestrator.Attempt{Provider: provider, Outcome: outcome, Status: document.StatusCode, DurationMS: time.Since(started).Milliseconds(), Classification: string(classification.Kind), Detail: classification.Reason})
		if classification.Kind == classify.Blocked || classification.Kind == classify.Retryable {
			continue
		}
		report := Analyze(request.URL, document)
		report.Attempts = attempts
		if classification.Kind == classify.Dead {
			report.Outcome = "dead"
		} else if request.Profile == "" && provider == page.ProviderPatchright && len(attempts) > 1 {
			s.routes.Record(parsed.Hostname(), provider)
		}
		return reportView(report, request.Verbose), nil
	}
	if last.Traffic != nil {
		report := Analyze(request.URL, last)
		report.Outcome = "blocked"
		report.Attempts = attempts
		report.Warnings = append(report.Warnings, "page remained blocked after browser escalation")
		return reportView(report, request.Verbose), nil
	}
	if len(attempts) > 0 {
		details := make([]string, 0, len(attempts))
		for _, attempt := range attempts {
			details = append(details, string(attempt.Provider)+": "+attempt.Detail)
		}
		return Report{}, errors.New("discovery failed: " + strings.Join(details, "; "))
	}
	return Report{}, errors.New("discovery failed: no browser capture available")
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

func (s *Service) WithProfiles(directory string) *Service {
	s.profilesDirectory = directory
	return s
}
