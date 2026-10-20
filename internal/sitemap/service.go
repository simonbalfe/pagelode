package sitemap

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/simonbalfe/pagelode/internal/limit"
	"github.com/simonbalfe/pagelode/internal/orchestrator"
	"github.com/simonbalfe/pagelode/internal/page"
)

const (
	maximumSitemaps  = 25
	sitemapBatchSize = 4
	fetchTimeout     = 10 * time.Second
)

type Service struct {
	fetcher orchestrator.Fetcher
	limiter *limit.Limiter
	browser Browser
}

func New(fetcher orchestrator.Fetcher, limiter *limit.Limiter) *Service {
	return &Service{fetcher: fetcher, limiter: limiter}
}

func (s *Service) WithBrowser(browser Browser) *Service {
	s.browser = browser
	return s
}

type run struct {
	request      Request
	scope        scope
	include      globs
	exclude      globs
	seen         map[string]bool
	endpointSeen map[string]bool
	report       Report
	pageRead     bool
}

func (s *Service) Map(ctx context.Context, request Request) (Report, error) {
	request, err := request.Validate()
	if err != nil {
		return Report{}, err
	}
	start, err := url.Parse(request.URL)
	if err != nil {
		return Report{}, err
	}
	started := time.Now()
	deadline, cancel := context.WithTimeout(ctx, time.Duration(request.MaxDurationMS)*time.Millisecond)
	defer cancel()
	current := &run{
		request:      request,
		scope:        newScope(start, request.Subdomains),
		include:      compileGlobs(request.Include),
		exclude:      compileGlobs(request.Exclude),
		seen:         map[string]bool{},
		endpointSeen: map[string]bool{},
		report:       Report{URL: request.URL, Outcome: "ok", URLs: []Entry{}},
	}

	origin := url.URL{Scheme: start.Scheme, Host: start.Host}
	queue, visited := []string{}, map[string]bool{}
	enqueue := func(raw, base string) {
		normalized := current.scope.normalize(raw, base)
		if normalized != "" && !visited[normalized] {
			visited[normalized] = true
			queue = append(queue, normalized)
		}
	}
	robotsURL := origin.String() + "/robots.txt"
	robots, err := s.load(deadline, robotsURL)
	if errors.Is(err, limit.ErrSaturated) {
		return Report{}, err
	}
	var rules robotsRules
	if err == nil && robots.StatusCode < 400 {
		rules = parseRobotsRules(robots.Text)
		for _, location := range robotsSitemaps(robots.Text) {
			enqueue(location, robotsURL)
		}
	}
	if len(queue) == 0 {
		enqueue("/sitemap.xml", origin.String())
		enqueue("/sitemap_index.xml", origin.String())
	}

	fetched := 0
	for len(queue) > 0 && !current.full() && deadline.Err() == nil {
		if fetched >= maximumSitemaps {
			current.warn("sitemap limit reached")
			break
		}
		size := min(sitemapBatchSize, len(queue), maximumSitemaps-fetched)
		batch := queue[:size]
		queue = queue[size:]
		fetched += size
		for _, result := range s.loadAll(deadline, batch) {
			if errors.Is(result.err, limit.ErrSaturated) {
				return Report{}, result.err
			}
			for _, child := range current.consumeSitemap(result) {
				enqueue(child, result.finalURL())
			}
		}
	}

	if !current.full() && deadline.Err() == nil {
		if err := s.crawl(deadline, current, request.URL, rules); err != nil {
			return Report{}, err
		}
	}

	if ctx.Err() != nil {
		return Report{}, ctx.Err()
	}
	if deadline.Err() != nil {
		current.warn("duration limit reached")
		current.report.Summary.Limited = true
	}
	if len(current.report.URLs) == 0 && current.report.Summary.SitemapsRead == 0 && !current.pageRead {
		current.report.Outcome = "failed"
	}
	current.report.Summary.URLsFound = len(current.report.URLs)
	current.report.Templates = templatesOf(current.report.URLs)
	current.report.Summary.DurationMS = time.Since(started).Milliseconds()
	return current.report, nil
}

type loaded struct {
	target   string
	document page.Document
	err      error
}

func (l loaded) finalURL() string {
	if l.document.FinalURL != "" {
		return l.document.FinalURL
	}
	return l.target
}

func (s *Service) load(ctx context.Context, target string) (page.Document, error) {
	pageCtx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()
	var document page.Document
	err := s.limiter.Run(pageCtx, func() error {
		var err error
		document, err = s.fetcher.Fetch(pageCtx, target, page.Session{})
		return err
	})
	return document, err
}

func (s *Service) loadAll(ctx context.Context, targets []string) []loaded {
	results := make([]loaded, len(targets))
	var workers sync.WaitGroup
	for i, target := range targets {
		workers.Go(func() {
			results[i].target = target
			results[i].document, results[i].err = s.load(ctx, target)
		})
	}
	workers.Wait()
	return results
}

func (r *run) full() bool {
	return len(r.report.URLs) >= r.request.Limit
}

func (r *run) warn(message string) {
	for _, existing := range r.report.Warnings {
		if existing == message {
			return
		}
	}
	r.report.Warnings = append(r.report.Warnings, message)
}

func (r *run) permitted(normalized string) bool {
	parsed, err := url.Parse(normalized)
	if err != nil {
		return false
	}
	return (len(r.include) == 0 || r.include.match(parsed.Path)) && !r.exclude.match(parsed.Path)
}

func (r *run) add(normalized, source, lastModified string) {
	if normalized == "" || r.seen[normalized] || !r.permitted(normalized) {
		return
	}
	if r.full() {
		r.report.Summary.Limited = true
		return
	}
	r.seen[normalized] = true
	r.report.URLs = append(r.report.URLs, Entry{URL: normalized, Source: source, LastModified: strings.TrimSpace(lastModified)})
}

// consumeSitemap records the page URLs of a sitemap and returns the locations of child sitemaps.
func (r *run) consumeSitemap(result loaded) []string {
	document := result.document
	if result.err != nil || document.StatusCode >= 400 {
		return nil
	}
	body := document.Text
	if body == "" {
		body = document.HTML
	}
	parsed, ok := parseSitemap(body)
	if !ok {
		if document.Type == page.ContentUnknown && strings.HasSuffix(strings.ToLower(result.target), ".gz") {
			r.warn(fmt.Sprintf("compressed sitemap skipped: %s", result.target))
		}
		return nil
	}
	r.report.Summary.SitemapsRead++
	base := result.finalURL()
	for _, entry := range parsed.URLs {
		r.add(r.scope.normalize(entry.Location, base), sourceSitemap, entry.LastModified)
	}
	children := make([]string, 0, len(parsed.Sitemaps))
	for _, entry := range parsed.Sitemaps {
		children = append(children, entry.Location)
	}
	return children
}
