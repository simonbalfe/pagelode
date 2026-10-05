package emails

import (
	"net/url"
	"sort"
	"time"
)

type search struct {
	request           Request
	scope             *url.URL
	report            Report
	queue             []target
	seen              map[string]bool
	visited           map[string]bool
	addresses         map[string]int
	sitemapRequests   int
	candidatesDropped bool
	sitemapLimited    bool
	depthLimited      bool
}

func (s *search) add(item target) {
	if s.seen[item.url] {
		return
	}
	if item.depth > 2 {
		s.depthLimited = true
		return
	}
	if len(s.seen) >= maxCandidates {
		s.candidatesDropped = true
		return
	}
	s.seen[item.url] = true
	s.queue = append(s.queue, item)
}

func (s *search) take(concurrency int) []target {
	batch := []target{}
	pages := len(s.report.Pages)
	for len(s.queue) > 0 && len(batch) < concurrency {
		item := s.queue[0]
		if !item.sitemap && pages >= s.request.MaxPages {
			break
		}
		s.queue = s.queue[1:]
		if s.visited[item.url] {
			continue
		}
		if item.sitemap {
			if s.sitemapRequests >= 4 {
				s.sitemapLimited = true
				continue
			}
			s.sitemapRequests++
		} else {
			pages++
		}
		s.visited[item.url] = true
		batch = append(batch, item)
	}
	return batch
}

func (s *search) consume(result loaded) {
	document := result.document
	finalURL := document.FinalURL
	if finalURL == "" {
		finalURL = result.target.url
	}
	normalized := scopedURL(finalURL, finalURL, s.scope)
	if result.target.sitemap {
		if result.err != nil || document.StatusCode >= 400 || normalized == "" {
			return
		}
		body := document.Text
		if body == "" {
			body = document.HTML
		}
		for _, candidate := range sitemapLinks(body, finalURL, s.scope) {
			s.add(candidate)
		}
		return
	}
	item := Page{URL: publicURL(result.target.url), FinalURL: publicURL(finalURL), Provider: document.Provider, Status: document.StatusCode, Outcome: "ok"}
	if result.err != nil || document.StatusCode >= 400 || normalized == "" {
		item.Outcome = "failed"
		s.report.Summary.PagesFailed++
		s.report.Pages = append(s.report.Pages, item)
		return
	}
	s.report.Pages = append(s.report.Pages, item)
	s.visited[normalized] = true
	document.FinalURL = finalURL
	for _, found := range extractAddresses(document) {
		s.record(found, finalURL)
	}
	for _, candidate := range pageLinks(document.HTML, finalURL, s.scope, result.target.depth+1) {
		s.add(candidate)
	}
}

func (s *search) record(found match, base string) {
	position, exists := s.addresses[found.address]
	if !exists {
		if len(s.report.Emails) >= s.request.MaxEmails {
			s.warn("email limit reached")
			return
		}
		position = len(s.report.Emails)
		s.addresses[found.address] = position
		s.report.Emails = append(s.report.Emails, Address{Address: found.address, Sources: []Source{}})
	}
	sourceURL := found.source
	if sourceURL == "" {
		sourceURL = base
	}
	source := Source{URL: publicURL(sourceURL), FoundIn: found.kind}
	for _, existing := range s.report.Emails[position].Sources {
		if existing == source {
			return
		}
	}
	s.report.Emails[position].Sources = append(s.report.Emails[position].Sources, source)
}

func (s *search) warn(message string) {
	for _, existing := range s.report.Warnings {
		if existing == message {
			return
		}
	}
	s.report.Summary.Limited = true
	s.report.Warnings = append(s.report.Warnings, message)
}

func (s *search) finish(started time.Time) Report {
	if len(s.report.Emails) >= s.request.MaxEmails {
		s.warn("email limit reached")
	}
	if len(s.queue) > 0 && len(s.report.Pages) >= s.request.MaxPages {
		s.warn("page limit reached")
	}
	if s.candidatesDropped {
		s.warn("candidate URL limit reached")
	}
	if s.sitemapLimited {
		s.warn("sitemap request limit reached")
	}
	if s.depthLimited {
		s.warn("crawl depth limit reached")
	}
	if s.report.Summary.PagesFailed > 0 || s.report.Summary.Limited {
		s.report.Outcome = "partial"
	}
	if len(s.report.Pages) > 0 && s.report.Summary.PagesFailed == len(s.report.Pages) {
		s.report.Outcome = "failed"
	}
	s.report.Summary.PagesVisited = len(s.report.Pages)
	s.report.Summary.EmailsFound = len(s.report.Emails)
	s.report.Summary.DurationMS = time.Since(started).Milliseconds()
	sort.Slice(s.report.Emails, func(i, j int) bool { return s.report.Emails[i].Address < s.report.Emails[j].Address })
	for i := range s.report.Emails {
		sources := s.report.Emails[i].Sources
		sort.Slice(sources, func(i, j int) bool {
			if sources[i].URL == sources[j].URL {
				return sources[i].FoundIn < sources[j].FoundIn
			}
			return sources[i].URL < sources[j].URL
		})
	}
	return s.report
}
