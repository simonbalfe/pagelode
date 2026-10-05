package emails

import (
	"errors"
	"net/url"
	"strings"

	"github.com/simonbalfe/pagelode/internal/page"
	"github.com/simonbalfe/pagelode/internal/profile"
)

type Request struct {
	URL           string `json:"url"`
	MaxPages      int    `json:"maxPages,omitempty"`
	MaxEmails     int    `json:"maxEmails,omitempty"`
	MaxDurationMS int    `json:"maxDurationMs,omitempty"`
	Profile       string `json:"profile,omitempty"`
	Render        string `json:"render,omitempty"`
}

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

type Report struct {
	URL      string    `json:"url"`
	Outcome  string    `json:"outcome"`
	Emails   []Address `json:"emails"`
	Pages    []Page    `json:"pages"`
	Summary  Summary   `json:"summary"`
	Warnings []string  `json:"warnings,omitempty"`
}

type Address struct {
	Address string   `json:"address"`
	Sources []Source `json:"sources"`
}

type Source struct {
	URL     string `json:"url"`
	FoundIn string `json:"foundIn"`
}

type Page struct {
	URL      string        `json:"url"`
	FinalURL string        `json:"finalUrl,omitempty"`
	Provider page.Provider `json:"provider,omitempty"`
	Status   int           `json:"status"`
	Outcome  string        `json:"outcome"`
}

type Summary struct {
	PagesVisited int   `json:"pagesVisited"`
	PagesFailed  int   `json:"pagesFailed"`
	EmailsFound  int   `json:"emailsFound"`
	DurationMS   int64 `json:"durationMs"`
	Limited      bool  `json:"limited"`
}

type Options struct {
	ProfileDirectory string
	Render           string
}
