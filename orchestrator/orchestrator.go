package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/simonbalfe/pagelode/classify"
	"github.com/simonbalfe/pagelode/extract"
	"github.com/simonbalfe/pagelode/limit"
	"github.com/simonbalfe/pagelode/memory"
	"github.com/simonbalfe/pagelode/page"
)

const maximumContentCharacters = 12_000

const OutcomeNeedsBrowser = "needs_browser"

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
	http       Fetcher
	browser    Fetcher
	capsolver  Fetcher
	routes     *memory.Routes
	clearances *memory.Clearances
}

func New(httpFetcher Fetcher, browserFetcher Fetcher, capSolverFetcher Fetcher, routes *memory.Routes, clearances *memory.Clearances) *Service {
	return &Service{
		http:       httpFetcher,
		browser:    browserFetcher,
		capsolver:  capSolverFetcher,
		routes:     routes,
		clearances: clearances,
	}
}

func (s *Service) Extract(ctx context.Context, input string) (Result, error) {
	state, err := newRunState(input)
	if err != nil {
		return Result{}, err
	}
	result, session, done, err := s.httpStage(ctx, &state)
	if done || err != nil {
		return result, err
	}
	return s.tryPatchright(ctx, &state, session)
}

func (s *Service) ExtractHTTP(ctx context.Context, input string) (Result, error) {
	state, err := newRunState(input)
	if err != nil {
		return Result{}, err
	}
	result, _, done, err := s.httpStage(ctx, &state)
	if done || err != nil {
		return result, err
	}
	return needsBrowserResult(state), nil
}

func (s *Service) ExtractBrowser(ctx context.Context, input string) (Result, error) {
	state, err := newRunState(input)
	if err != nil {
		return Result{}, err
	}
	state.deferOnCapacity = true
	return s.tryPatchright(ctx, &state, page.Session{})
}

func (s *Service) httpStage(ctx context.Context, state *runState) (Result, page.Session, bool, error) {
	if result, done := s.tryReplay(ctx, state); done {
		return result, page.Session{}, true, nil
	}
	if preferred, ok := s.routes.Preferred(state.host); ok && preferred == page.ProviderPatchright && s.browser != nil {
		return Result{}, page.Session{}, false, nil
	}

	document, classification, ok := s.attempt(ctx, state, page.ProviderTLS, s.http, page.Session{})
	if !ok {
		return Result{}, page.Session{}, false, nil
	}
	if result, done := s.resolveDocument(state, document, classification); done {
		return result, page.Session{}, true, nil
	}

	switch classification.Kind {
	case classify.Dead:
		return failedResult(*state, document, "dead"), page.Session{}, true, nil
	case classify.Blocked, classify.NeedsRender, classify.Retryable, classify.Accept:
		return Result{}, document.Session, false, nil
	default:
		return Result{}, page.Session{}, true, fmt.Errorf("extract: unsupported classification %q", classification.Kind)
	}
}

func (s *Service) tryReplay(ctx context.Context, state *runState) (Result, bool) {
	session, ok := s.clearances.Get(state.host)
	if !ok {
		return Result{}, false
	}
	document, classification, ok := s.attempt(ctx, state, page.ProviderReplay, s.http, session)
	if ok {
		document.Provider = page.ProviderReplay
		if result, done := s.resolveDocument(state, document, classification); done {
			return result, true
		}
		if classification.Kind == classify.Dead {
			return failedResult(*state, document, "dead"), true
		}
	}
	s.clearances.Forget(state.host)
	return Result{}, false
}

func (s *Service) tryPatchright(ctx context.Context, state *runState, session page.Session) (Result, error) {
	if s.browser == nil || state.tried[page.ProviderPatchright] {
		return s.tryCapSolver(ctx, state, session)
	}
	document, classification, ok := s.attempt(ctx, state, page.ProviderPatchright, s.browser, session)
	if !ok {
		if state.capacityErr != nil {
			return Result{}, state.capacityErr
		}
		return s.tryCapSolver(ctx, state, session)
	}
	if result, done := s.resolveDocument(state, document, classification); done {
		s.routes.Record(state.host, page.ProviderPatchright)
		s.clearances.Record(state.host, document.Session)
		return result, nil
	}
	if classification.Kind == classify.Dead {
		return failedResult(*state, document, "dead"), nil
	}
	return s.tryCapSolver(ctx, state, document.Session)
}

func (s *Service) tryCapSolver(ctx context.Context, state *runState, session page.Session) (Result, error) {
	if s.capsolver == nil || state.tried[page.ProviderCapSolver] {
		return failedResult(*state, page.Document{Provider: page.ProviderCapSolver, Type: page.ContentUnknown}, "failed"), nil
	}
	document, classification, ok := s.attempt(ctx, state, page.ProviderCapSolver, s.capsolver, session)
	if !ok {
		return failedResult(*state, page.Document{Provider: page.ProviderCapSolver, Type: page.ContentUnknown}, "failed"), nil
	}
	if result, done := s.resolveDocument(state, document, classification); done {
		return result, nil
	}
	if classification.Kind == classify.Dead {
		return failedResult(*state, document, "dead"), nil
	}
	return failedResult(*state, document, "failed"), nil
}

func (s *Service) attempt(ctx context.Context, state *runState, provider page.Provider, fetcher Fetcher, session page.Session) (page.Document, classify.Result, bool) {
	state.tried[provider] = true
	started := time.Now()
	document, err := fetcher.Fetch(ctx, state.target, session)
	duration := time.Since(started).Milliseconds()
	if err != nil {
		state.attempts = append(state.attempts, Attempt{Provider: provider, Outcome: "error", DurationMS: duration, Detail: concise(err)})
		if state.deferOnCapacity && provider == page.ProviderPatchright && (errors.Is(err, limit.ErrSaturated) || errors.Is(err, limit.ErrUnavailable)) {
			state.capacityErr = fmt.Errorf("extract: browser capacity: %w", err)
		}
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
	target          string
	host            string
	tried           map[page.Provider]bool
	attempts        []Attempt
	deferOnCapacity bool
	capacityErr     error
}

func newRunState(input string) (runState, error) {
	target, err := parseURL(input)
	if err != nil {
		return runState{}, err
	}
	return runState{target: target.String(), host: target.Hostname(), tried: make(map[page.Provider]bool)}, nil
}

func needsBrowserResult(state runState) Result {
	return Result{
		URL:         state.target,
		ContentType: page.ContentUnknown,
		Outcome:     OutcomeNeedsBrowser,
		Links:       []string{},
		Attempts:    state.attempts,
	}
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
