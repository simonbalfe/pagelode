package emails

import (
	"errors"
	"net/url"
	"strings"

	"github.com/simonbalfe/pagelode/page"
	"github.com/simonbalfe/pagelode/profile"
)

// Request describes one email crawl rooted at URL.
type Request struct {
	URL           string `json:"url"`
	MaxPages      int    `json:"maxPages,omitempty"`
	MaxEmails     int    `json:"maxEmails,omitempty"`
	MaxDurationMS int    `json:"maxDurationMs,omitempty"`
	Profile       string `json:"profile,omitempty"`
	Render        string `json:"render,omitempty"`
}

// Validate returns a normalized copy of r with defaults applied, or an error describing the first invalid field.
func (r Request) Validate() (Request, error) {
	if !strings.Contains(r.URL, "://") {
		r.URL = "https://" + r.URL
	}
	target, err := url.ParseRequestURI(r.URL)
	if err != nil || target.Hostname() == "" || target.User != nil || target.Scheme != "http" && target.Scheme != "https" {
		return Request{}, errors.New("email search URL must be HTTP(S) without credentials")
	}
	if target.Path == "" {
		target.Path = "/"
	}
	target.Fragment = ""
	r.URL = target.String()
	if r.MaxPages == 0 {
		r.MaxPages = 20
	}
	if r.MaxPages < 1 || r.MaxPages > 100 {
		return Request{}, errors.New("maxPages must be between 1 and 100")
	}
	if r.MaxEmails == 0 {
		r.MaxEmails = 100
	}
	if r.MaxEmails < 1 || r.MaxEmails > 1000 {
		return Request{}, errors.New("maxEmails must be between 1 and 1000")
	}
	if r.MaxDurationMS == 0 {
		r.MaxDurationMS = 30000
	}
	if r.MaxDurationMS < 1000 || r.MaxDurationMS > 120000 {
		return Request{}, errors.New("maxDurationMs must be between 1000 and 120000")
	}
	if r.Render == "" {
		r.Render = "auto"
	}
	if r.Render != "auto" && r.Render != "never" && r.Render != "always" {
		return Request{}, errors.New("render must be auto, never, or always")
	}
	if r.Profile != "" {
		if err := profile.Validate(r.Profile); err != nil {
			return Request{}, err
		}
		if r.Render == "never" {
			return Request{}, errors.New("authenticated profiles require browser rendering")
		}
	}
	return r, nil
}

// Report is the result of an email crawl.
type Report struct {
	URL      string    `json:"url"`
	Outcome  string    `json:"outcome"`
	Emails   []Address `json:"emails"`
	Pages    []Page    `json:"pages"`
	Summary  Summary   `json:"summary"`
	Warnings []string  `json:"warnings,omitempty"`
}

// Address is one discovered email address and every place it was found.
type Address struct {
	Address string   `json:"address"`
	Sources []Source `json:"sources"`
}

// Source is one page where an address was found and the part of the page it came from.
type Source struct {
	URL     string `json:"url"`
	FoundIn string `json:"foundIn"`
}

// Page records the outcome of loading one crawled page.
type Page struct {
	URL      string        `json:"url"`
	FinalURL string        `json:"finalUrl,omitempty"`
	Provider page.Provider `json:"provider,omitempty"`
	Status   int           `json:"status"`
	Outcome  string        `json:"outcome"`
}

// Summary totals a crawl; Limited reports whether a page, email, or duration limit stopped it early.
type Summary struct {
	PagesVisited int   `json:"pagesVisited"`
	PagesFailed  int   `json:"pagesFailed"`
	EmailsFound  int   `json:"emailsFound"`
	DurationMS   int64 `json:"durationMs"`
	Limited      bool  `json:"limited"`
}

// Options controls how a single page is loaded.
type Options struct {
	Profile string
	Render  string
}
