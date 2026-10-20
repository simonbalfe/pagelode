package sitemap

import (
	"errors"
	"net/url"
	"strings"

	"github.com/simonbalfe/pagelode/urlshape"
)

const (
	defaultLimit      = 500
	maximumLimit      = 5000
	maximumPatterns   = 20
	maximumPatternLen = 200
)

// Request describes a site map run. Zero values for Limit, MaxDurationMS,
// Depth, and Render select defaults in Validate. Include and Exclude are path
// glob patterns; Render is "never", "auto", or "always".
type Request struct {
	URL           string   `json:"url"`
	Limit         int      `json:"limit,omitempty"`
	MaxDurationMS int      `json:"maxDurationMs,omitempty"`
	Subdomains    bool     `json:"subdomains,omitempty"`
	Include       []string `json:"include,omitempty"`
	Exclude       []string `json:"exclude,omitempty"`
	Depth         int      `json:"depth,omitempty"`
	Render        string   `json:"render,omitempty"`
	Explore       bool     `json:"explore,omitempty"`
}

// Validate returns a copy of r with the URL normalised and defaults applied,
// or an error describing the first invalid field.
func (r Request) Validate() (Request, error) {
	if !strings.Contains(r.URL, "://") {
		r.URL = "https://" + r.URL
	}
	target, err := url.ParseRequestURI(r.URL)
	if err != nil || target.Hostname() == "" || target.User != nil || target.Scheme != "http" && target.Scheme != "https" {
		return Request{}, errors.New("map URL must be HTTP(S) without credentials")
	}
	if target.Path == "" {
		target.Path = "/"
	}
	target.Fragment = ""
	r.URL = target.String()
	if r.Limit == 0 {
		r.Limit = defaultLimit
	}
	if r.Limit < 1 || r.Limit > maximumLimit {
		return Request{}, errors.New("limit must be between 1 and 5000")
	}
	if r.MaxDurationMS == 0 {
		r.MaxDurationMS = 30000
	}
	if r.MaxDurationMS < 1000 || r.MaxDurationMS > 120000 {
		return Request{}, errors.New("maxDurationMs must be between 1000 and 120000")
	}
	for _, patterns := range [][]string{r.Include, r.Exclude} {
		if len(patterns) > maximumPatterns {
			return Request{}, errors.New("include and exclude accept at most 20 patterns each")
		}
		for _, pattern := range patterns {
			if !strings.HasPrefix(pattern, "/") || len(pattern) > maximumPatternLen {
				return Request{}, errors.New("path patterns must start with / and be at most 200 characters")
			}
		}
	}
	if r.Depth == 0 {
		r.Depth = defaultDepth
	}
	if r.Depth < 1 || r.Depth > maximumDepth {
		return Request{}, errors.New("depth must be between 1 and 5")
	}
	switch r.Render {
	case "":
		r.Render = renderNever
	case renderNever, renderAuto, renderAlways:
	default:
		return Request{}, errors.New("render must be never, auto, or always")
	}
	if r.Explore && r.Render == renderNever {
		return Request{}, errors.New("explore needs render auto or always")
	}
	return r, nil
}

// Report is the result of Map. Outcome is "ok", or "failed" when no sitemap
// or page could be read and no URLs were found.
type Report struct {
	URL       string              `json:"url"`
	Outcome   string              `json:"outcome"`
	URLs      []Entry             `json:"urls"`
	Summary   Summary             `json:"summary"`
	Warnings  []string            `json:"warnings,omitempty"`
	Endpoints []string            `json:"endpoints,omitempty"`
	Templates []urlshape.Template `json:"templates,omitempty"`
}

// Entry is a discovered URL; Source records how it was found ("sitemap",
// "page", "link", "rendered", or "explore").
type Entry struct {
	URL          string `json:"url"`
	Source       string `json:"source"`
	LastModified string `json:"lastModified,omitempty"`
}

// Summary holds counters for a Map run; Limited reports that a URL, page, or
// duration limit cut the run short.
type Summary struct {
	URLsFound     int   `json:"urlsFound"`
	SitemapsRead  int   `json:"sitemapsRead"`
	PagesCrawled  int   `json:"pagesCrawled"`
	PagesRendered int   `json:"pagesRendered,omitempty"`
	RobotsSkipped int   `json:"robotsSkipped,omitempty"`
	DurationMS    int64 `json:"durationMs"`
	Limited       bool  `json:"limited"`
}

const (
	sourceSitemap  = "sitemap"
	sourcePage     = "page"
	sourceLink     = "link"
	sourceRendered = "rendered"
	sourceExplore  = "explore"
)
