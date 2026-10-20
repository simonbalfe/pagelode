package sitemap

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/simonbalfe/pagelode/pkg/fetch"
)

const (
	defaultDepth       = 2
	maximumDepth       = 5
	crawlBatchSize     = 6
	maximumCrawlPages  = 200
	maximumRenders     = 25
	maximumExplores    = 5
	maximumScripts     = 20
	maximumEndpoints   = 200
	shapeSampleLimit   = 20
	similarityDistance = 3
	similarityMinText  = 200
	templateThreshold  = 10
	browserTimeout     = 45 * time.Second
)

const (
	renderNever  = "never"
	renderAuto   = "auto"
	renderAlways = "always"
)

type Browser interface {
	Fetch(context.Context, string, fetch.Session) (fetch.Document, error)
	Explore(context.Context, string, fetch.Session, fetch.ExploreOptions) (fetch.Document, error)
}

type crawlTarget struct {
	url   string
	depth int
}

type crawled struct {
	target   crawlTarget
	document fetch.Document
	rendered bool
	err      error
}

type crawler struct {
	service  *Service
	run      *run
	rules    robotsRules
	queued   map[string]bool
	scripts  map[string]bool
	sampler  *Sampler
	index    *DuplicateIndex
	renders  atomic.Int32
	explores atomic.Int32
}

func (s *Service) crawl(ctx context.Context, current *run, start string, rules robotsRules) error {
	c := &crawler{
		service: s,
		run:     current,
		rules:   rules,
		queued:  map[string]bool{},
		scripts: map[string]bool{},
		sampler: NewSampler(shapeSampleLimit),
		index:   NewDuplicateIndex(similarityDistance),
	}
	if (current.request.Render != renderNever) && s.browser == nil {
		current.warn("browser rendering is unavailable; pages were loaded over HTTP only")
	}
	c.queued[current.scope.normalize(start, start)] = true

	level := []crawlTarget{{url: start}}
	for depth := 0; depth < current.request.Depth && len(level) > 0; depth++ {
		expand := depth+1 < current.request.Depth
		var next []crawlTarget
		var scripts []string
		for len(level) > 0 && !current.full() && ctx.Err() == nil {
			if current.report.Summary.PagesCrawled >= maximumCrawlPages {
				current.warn("crawl page limit reached")
				break
			}
			size := min(crawlBatchSize, len(level), maximumCrawlPages-current.report.Summary.PagesCrawled)
			batch := level[:size]
			level = level[size:]
			for _, result := range c.fetchAll(ctx, batch) {
				if errors.Is(result.err, fetch.ErrSaturated) {
					return result.err
				}
				c.consume(result, expand, &next, &scripts)
			}
		}
		if err := c.readScripts(ctx, scripts); err != nil {
			return err
		}
		level = next
	}
	return nil
}

func (c *crawler) fetchAll(ctx context.Context, targets []crawlTarget) []crawled {
	results := make([]crawled, len(targets))
	done := make(chan struct{}, len(targets))
	for i, target := range targets {
		go func() {
			results[i] = c.fetch(ctx, target)
			done <- struct{}{}
		}()
	}
	for range targets {
		<-done
	}
	return results
}

func (c *crawler) fetch(ctx context.Context, target crawlTarget) crawled {
	request := c.run.request
	browser := c.service.browser
	if browser != nil && request.Explore && c.explores.Add(1) <= maximumExplores {
		document, err := c.service.runBrowser(ctx, func(ctx context.Context) (fetch.Document, error) {
			return browser.Explore(ctx, target.url, fetch.Session{}, fetch.ExploreOptions{})
		})
		if err == nil || errors.Is(err, fetch.ErrSaturated) {
			return crawled{target: target, document: document, rendered: true, err: err}
		}
	} else if browser != nil && request.Render == renderAlways && c.renders.Add(1) <= maximumRenders {
		document, err := c.service.runBrowser(ctx, func(ctx context.Context) (fetch.Document, error) {
			return browser.Fetch(ctx, target.url, fetch.Session{})
		})
		if err == nil || errors.Is(err, fetch.ErrSaturated) {
			return crawled{target: target, document: document, rendered: true, err: err}
		}
	}

	document, err := c.service.load(ctx, target.url)
	if err == nil && browser != nil && request.Render == renderAuto && document.Type == fetch.ContentHTML &&
		fetch.Classify(document).Kind == fetch.NeedsRender && c.renders.Add(1) <= maximumRenders {
		rendered, renderErr := c.service.runBrowser(ctx, func(ctx context.Context) (fetch.Document, error) {
			return browser.Fetch(ctx, target.url, fetch.Session{})
		})
		if renderErr == nil {
			return crawled{target: target, document: rendered, rendered: true}
		}
		if errors.Is(renderErr, fetch.ErrSaturated) {
			return crawled{target: target, err: renderErr}
		}
	}
	return crawled{target: target, document: document, err: err}
}

func (c *crawler) consume(result crawled, expand bool, next *[]crawlTarget, scripts *[]string) {
	r := c.run
	document, target := result.document, result.target
	if result.err != nil || document.StatusCode >= 400 {
		if target.depth == 0 {
			r.warn("start page could not be loaded")
		}
		return
	}
	finalURL := document.FinalURL
	if finalURL == "" {
		finalURL = target.url
	}
	normalized := r.scope.normalize(finalURL, finalURL)
	if normalized == "" {
		if target.depth == 0 {
			r.warn("start page redirected outside the mapped site")
		}
		return
	}
	r.report.Summary.PagesCrawled++
	if result.rendered {
		r.report.Summary.PagesRendered++
	}
	source := sourceLink
	switch {
	case result.rendered:
		source = sourceRendered
	case target.depth == 0:
		source = sourcePage
	}
	if target.depth == 0 {
		r.pageRead = true
		r.add(normalized, source, "")
	}
	if document.Type != fetch.ContentHTML {
		return
	}

	duplicate := false
	if text := PageText(document.HTML); len(text) >= similarityMinText {
		duplicate = c.index.Seen(Fingerprint(text))
	}
	for _, href := range pageLinks(document.HTML) {
		link := r.scope.normalize(href, finalURL)
		r.add(link, source, "")
		if expand && !duplicate {
			c.enqueue(link, target.depth+1, next)
		}
	}
	for _, explored := range document.ExploredURLs {
		link := r.scope.normalize(explored, finalURL)
		r.add(link, sourceExplore, "")
		if expand && !duplicate {
			c.enqueue(link, target.depth+1, next)
		}
	}

	if base, err := url.Parse(finalURL); err == nil {
		for _, script := range ScriptURLs(document.HTML, base) {
			if !c.scripts[script] && len(c.scripts) < maximumScripts {
				c.scripts[script] = true
				*scripts = append(*scripts, script)
			}
		}
	}
	r.addEndpoints(ScriptEndpoints(inlineScripts(document.HTML)), finalURL)
}

func (c *crawler) enqueue(link string, depth int, next *[]crawlTarget) {
	if link == "" || c.queued[link] || !c.run.permitted(link) {
		return
	}
	if c.rules.blocks(link) {
		c.queued[link] = true
		c.run.report.Summary.RobotsSkipped++
		return
	}
	if !c.sampler.Allow(link) {
		return
	}
	c.queued[link] = true
	*next = append(*next, crawlTarget{url: link, depth: depth})
}

func (c *crawler) readScripts(ctx context.Context, scripts []string) error {
	if len(scripts) == 0 || ctx.Err() != nil {
		return nil
	}
	for _, result := range c.service.loadAll(ctx, scripts) {
		if errors.Is(result.err, fetch.ErrSaturated) {
			return result.err
		}
		if result.err != nil || result.document.StatusCode >= 400 {
			continue
		}
		source := result.document.Text
		if source == "" {
			source = result.document.HTML
		}
		c.run.addEndpoints(ScriptEndpoints(source), result.finalURL())
	}
	return nil
}

func (s *Service) runBrowser(ctx context.Context, call func(context.Context) (fetch.Document, error)) (fetch.Document, error) {
	pageCtx, cancel := context.WithTimeout(ctx, browserTimeout)
	defer cancel()
	var document fetch.Document
	err := s.limiter.Run(pageCtx, func() error {
		var err error
		document, err = call(pageCtx)
		return err
	})
	return document, err
}

func (r *run) addEndpoints(paths []string, base string) {
	for _, path := range paths {
		endpoint := r.scope.normalize(path, base)
		if endpoint == "" || r.endpointSeen[endpoint] || len(r.report.Endpoints) >= maximumEndpoints {
			continue
		}
		r.endpointSeen[endpoint] = true
		r.report.Endpoints = append(r.report.Endpoints, endpoint)
	}
}

func inlineScripts(rawHTML string) string {
	dom, err := goquery.NewDocumentFromReader(strings.NewReader(rawHTML))
	if err != nil {
		return ""
	}
	var combined strings.Builder
	dom.Find("script:not([src])").Each(func(_ int, selection *goquery.Selection) {
		combined.WriteString(selection.Text())
		combined.WriteByte('\n')
	})
	return combined.String()
}
