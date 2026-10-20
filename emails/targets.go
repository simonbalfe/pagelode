package emails

import (
	"encoding/xml"
	"net/url"
	"path"
	"sort"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

const maxCandidates = 1000

type target struct {
	url     string
	depth   int
	score   int
	sitemap bool
}

func pageLinks(rawHTML, base string, scope *url.URL, depth int) []target {
	dom, err := goquery.NewDocumentFromReader(strings.NewReader(rawHTML))
	if err != nil {
		return nil
	}
	result := []target{}
	dom.Find("a[href]").EachWithBreak(func(_ int, selection *goquery.Selection) bool {
		href, _ := selection.Attr("href")
		normalized := scopedURL(href, base, scope)
		if normalized != "" {
			result = append(result, target{url: normalized, depth: depth, score: priority(normalized + " " + selection.Text())})
		}
		return len(result) < maxCandidates
	})
	return result
}

func scopedURL(raw, base string, scope *url.URL) string {
	parent, err := url.Parse(base)
	if err != nil {
		return ""
	}
	reference, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	current := parent.ResolveReference(reference)
	if current.User != nil || current.Scheme != "http" && current.Scheme != "https" || !inScope(current, scope) {
		return ""
	}
	current.Fragment = ""
	current.Host = strings.ToLower(current.Host)
	if current.Path == "" {
		current.Path = "/"
	}
	for _, segment := range strings.Split(strings.ToLower(current.Path), "/") {
		switch segment {
		case "logout", "log-out", "signout", "sign-out", "unsubscribe", "delete", "remove":
			return ""
		}
	}
	switch strings.ToLower(path.Ext(current.Path)) {
	case ".jpg", ".jpeg", ".png", ".gif", ".svg", ".webp", ".ico", ".css", ".js", ".pdf", ".zip", ".mp4", ".woff", ".woff2":
		return ""
	}
	query := current.Query()
	for name := range query {
		if strings.HasPrefix(strings.ToLower(name), "utm_") || name == "fbclid" || name == "gclid" {
			query.Del(name)
		}
	}
	current.RawQuery = query.Encode()
	return current.String()
}

func inScope(current, scope *url.URL) bool {
	host := func(value *url.URL) string { return strings.TrimPrefix(strings.ToLower(value.Hostname()), "www.") }
	return host(current) == host(scope) && current.Port() == scope.Port()
}

func priority(value string) int {
	lower := strings.ToLower(value)
	for i, word := range []string{"contact", "team", "staff", "directory", "people", "about", "location", "support"} {
		if strings.Contains(lower, word) {
			return 100 - i*10
		}
	}
	return 0
}

func orderTargets(queue []target) {
	sort.SliceStable(queue, func(i, j int) bool {
		if queue[i].score != queue[j].score {
			return queue[i].score > queue[j].score
		}
		if queue[i].depth != queue[j].depth {
			return queue[i].depth < queue[j].depth
		}
		return queue[i].url < queue[j].url
	})
}

func sitemapLinks(body, base string, scope *url.URL) []target {
	var document struct {
		XMLName xml.Name
		URLs    []struct {
			Location string `xml:"loc"`
		} `xml:"url"`
		Sitemaps []struct {
			Location string `xml:"loc"`
		} `xml:"sitemap"`
	}
	if xml.Unmarshal([]byte(body), &document) != nil {
		return nil
	}
	result := []target{}
	for _, entry := range document.URLs {
		normalized := scopedURL(entry.Location, base, scope)
		if normalized != "" && priority(normalized) > 0 {
			result = append(result, target{url: normalized, depth: 1, score: priority(normalized)})
		}
		if len(result) >= maxCandidates {
			return result
		}
	}
	for _, entry := range document.Sitemaps {
		normalized := scopedURL(entry.Location, base, scope)
		if normalized != "" {
			result = append(result, target{url: normalized, sitemap: true, score: 110})
		}
		if len(result) >= maxCandidates {
			return result
		}
	}
	return result
}

func publicURL(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	parsed.User = nil
	parsed.Fragment = ""
	query := parsed.Query()
	for name := range query {
		query.Set(name, "[redacted]")
	}
	parsed.RawQuery = query.Encode()
	return parsed.String()
}
