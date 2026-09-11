package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/simonbalfe/pagelode/internal/classify"
	"github.com/simonbalfe/pagelode/internal/extract"
	"github.com/simonbalfe/pagelode/internal/limit"
	"github.com/simonbalfe/pagelode/internal/memory"
	"github.com/simonbalfe/pagelode/internal/page"
)

const maximumContentCharacters = 12_000

type Fetcher interface {
	Fetch(context.Context, string, page.Session) (page.Document, error)
}

type Attempt struct {
	Provider       page.Provider `json:"provider"`
	Outcome        string        `json:"outcome"`
	Status         int           `json:"status,omitempty"`
	DurationMS     int64         `json:"durationMs"`
	Detail         string        `json:"detail,omitempty"`
	Classification string        `json:"classification,omitempty"`
	RenderScore    int           `json:"renderScore,omitempty"`
}

type Result struct {
	URL         string           `json:"url"`
	FinalURL    string           `json:"finalUrl,omitempty"`
	Title       string           `json:"title,omitempty"`
	Content     string           `json:"content"`
	ContentType page.ContentType `json:"contentType"`
	Provider    page.Provider    `json:"provider"`
	Outcome     string           `json:"outcome"`
	Links       []string         `json:"links"`
	Attempts    []Attempt        `json:"attempts"`
}

type Service struct {
	http           Fetcher
	rod            Fetcher
	patchright     Fetcher
	routes         *memory.Routes
	browserLimiter *limit.Limiter
	rodEnabled     bool
}

func New(httpFetcher Fetcher, rodFetcher Fetcher, patchrightFetcher Fetcher, routes *memory.Routes, browserLimiter *limit.Limiter, rodEnabled bool) *Service {
	return &Service{
		http:           httpFetcher,
		rod:            rodFetcher,
		patchright:     patchrightFetcher,
		routes:         routes,
		browserLimiter: browserLimiter,
		rodEnabled:     rodEnabled,
	}
}

func (s *Service) Extract(ctx context.Context, input string) (Result, error) {
	target, err := parseURL(input)
	if err != nil {
		return Result{}, err
	}
	state := runState{target: target.String(), host: target.Hostname(), tried: make(map[page.Provider]bool)}

	if preferred, ok := s.routes.Preferred(state.host); ok {
		if result, done := s.tryPreferred(ctx, &state, preferred); done {
			return result, nil
		}
	}

	document, classification, ok := s.attempt(ctx, &state, page.ProviderTLS, s.http, page.Session{})
	if !ok {
		return s.tryPatchright(ctx, &state, page.Session{})
	}
	if result, done := s.resolveDocument(&state, document, classification); done {
		return result, nil
	}

	switch classification.Kind {
	case classify.Dead:
		return failedResult(state, document, "dead"), nil
	case classify.Blocked:
		return s.tryPatchright(ctx, &state, document.Session)
	case classify.NeedsRender:
		if s.rodEnabled && s.rod != nil {
			rodDocument, rodClassification, rodOK := s.attemptBrowser(ctx, &state, page.ProviderRod, s.rod, document.Session)
			if rodOK {
				if result, done := s.resolveDocument(&state, rodDocument, rodClassification); done {
					s.routes.Record(state.host, page.ProviderRod)
					return result, nil
				}
				if rodClassification.Kind == classify.Dead {
					return failedResult(state, rodDocument, "dead"), nil
				}
			}
		}
		return s.tryPatchright(ctx, &state, document.Session)
	case classify.Retryable:
		return s.tryPatchright(ctx, &state, document.Session)
	case classify.Accept:
		return s.tryPatchright(ctx, &state, document.Session)
	default:
		return Result{}, fmt.Errorf("extract: unsupported classification %q", classification.Kind)
	}
}

func (s *Service) tryPreferred(ctx context.Context, state *runState, provider page.Provider) (Result, bool) {
	var fetcher Fetcher
	switch provider {
	case page.ProviderRod:
		if !s.rodEnabled {
			return Result{}, false
		}
		fetcher = s.rod
	case page.ProviderPatchright:
		fetcher = s.patchright
	default:
		return Result{}, false
	}
	if fetcher == nil {
		return Result{}, false
	}

	document, classification, ok := s.attemptBrowser(ctx, state, provider, fetcher, page.Session{})
	if !ok {
		return Result{}, false
	}
	if result, done := s.resolveDocument(state, document, classification); done {
		s.routes.Record(state.host, provider)
		return result, true
	}
	if classification.Kind == classify.Dead {
		return failedResult(*state, document, "dead"), true
	}
	if provider == page.ProviderRod && classification.Kind == classify.Blocked {
		result, err := s.tryPatchright(ctx, state, document.Session)
		if err == nil {
			return result, true
		}
	}
	return Result{}, false
}

func (s *Service) tryPatchright(ctx context.Context, state *runState, session page.Session) (Result, error) {
	if s.patchright == nil || state.tried[page.ProviderPatchright] {
		return failedResult(*state, page.Document{Provider: page.ProviderPatchright, Type: page.ContentUnknown}, "failed"), nil
	}
	document, classification, ok := s.attemptBrowser(ctx, state, page.ProviderPatchright, s.patchright, session)
	if !ok {
		return failedResult(*state, page.Document{Provider: page.ProviderPatchright, Type: page.ContentUnknown}, "failed"), nil
	}
	if result, done := s.resolveDocument(state, document, classification); done {
		s.routes.Record(state.host, page.ProviderPatchright)
		return result, nil
	}
	if classification.Kind == classify.Dead {
		return failedResult(*state, document, "dead"), nil
	}
	return failedResult(*state, document, "failed"), nil
}

func (s *Service) attemptBrowser(ctx context.Context, state *runState, provider page.Provider, fetcher Fetcher, session page.Session) (page.Document, classify.Result, bool) {
	var (
		document page.Document
		result   classify.Result
		ok       bool
	)
	err := s.browserLimiter.Run(ctx, func() error {
		document, result, ok = s.attempt(ctx, state, provider, fetcher, session)
		return nil
	})
	if err != nil {
		state.attempts = append(state.attempts, Attempt{Provider: provider, Outcome: "error", Detail: err.Error()})
		return page.Document{}, classify.Result{}, false
	}
	return document, result, ok
}

func (s *Service) attempt(ctx context.Context, state *runState, provider page.Provider, fetcher Fetcher, session page.Session) (page.Document, classify.Result, bool) {
	state.tried[provider] = true
	started := time.Now()
	document, err := fetcher.Fetch(ctx, state.target, session)
	duration := time.Since(started).Milliseconds()
	if err != nil {
		state.attempts = append(state.attempts, Attempt{Provider: provider, Outcome: "error", DurationMS: duration, Detail: concise(err)})
		return page.Document{}, classify.Result{}, false
	}

	classification := classify.Document(document)
	outcome := string(classification.Kind)
	if classification.Kind == classify.Accept {
		outcome = "ok"
	}
	state.attempts = append(state.attempts, Attempt{
		Provider:       provider,
		Outcome:        outcome,
		Status:         document.StatusCode,
		DurationMS:     duration,
		Detail:         classification.Reason,
		Classification: string(classification.Kind),
		RenderScore:    classification.RenderScore,
	})
	return document, classification, true
}

func (s *Service) resolveDocument(state *runState, document page.Document, classification classify.Result) (Result, bool) {
	if classification.Kind != classify.Accept {
		return Result{}, false
	}

	switch document.Type {
	case page.ContentText:
		if strings.TrimSpace(document.Text) == "" {
			return Result{}, false
		}
		return successResult(*state, document, bounded(document.Text), nil, document.Title), true
	case page.ContentHTML:
		extracted, err := extract.HTML(document.HTML, document.FinalURL)
		if err != nil || strings.TrimSpace(extracted.Content) == "" {
			return Result{}, false
		}
		title := document.Title
		if title == "" {
			title = extracted.Title
		}
		return successResult(*state, document, bounded(extracted.Content), extracted.Links, title), true
	default:
		return Result{}, false
	}
}

type runState struct {
	target   string
	host     string
	tried    map[page.Provider]bool
	attempts []Attempt
}

func successResult(state runState, document page.Document, content string, links []string, title string) Result {
	if links == nil {
		links = []string{}
	}
	return Result{
		URL:         state.target,
		FinalURL:    document.FinalURL,
		Title:       title,
		Content:     content,
		ContentType: document.Type,
		Provider:    document.Provider,
		Outcome:     "ok",
		Links:       links,
		Attempts:    state.attempts,
	}
}

func failedResult(state runState, document page.Document, outcome string) Result {
	return Result{
		URL:         state.target,
		FinalURL:    document.FinalURL,
		Title:       document.Title,
		Content:     "",
		ContentType: document.Type,
		Provider:    document.Provider,
		Outcome:     outcome,
		Links:       []string{},
		Attempts:    state.attempts,
	}
}

func parseURL(input string) (*url.URL, error) {
	if !strings.Contains(input, "://") {
		input = "https://" + input
	}
	parsed, err := url.ParseRequestURI(input)
	if err != nil {
		return nil, fmt.Errorf("extract: invalid URL: %w", err)
	}
	if parsed.Hostname() == "" || parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, errors.New("extract: URL must be absolute HTTP(S)")
	}
	return parsed, nil
}

func bounded(content string) string {
	content = strings.TrimSpace(content)
	runes := []rune(content)
	if len(runes) <= maximumContentCharacters {
		return content
	}
	return string(runes[:maximumContentCharacters]) + "\n\n[truncated]"
}

func concise(err error) string {
	message := err.Error()
	if index := strings.IndexByte(message, '\n'); index >= 0 {
		message = message[:index]
	}
	if len(message) > 300 {
		message = message[:300]
	}
	return message
}
