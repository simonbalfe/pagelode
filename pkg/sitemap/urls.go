package sitemap

import (
	"encoding/xml"
	"net/url"
	"path"
	"regexp"
	"strings"

	"github.com/PuerkitoBio/goquery"
	"github.com/simonbalfe/pagelode/pkg/urlshape"
)

const maximumPageLinks = 5000

type scope struct {
	host       string
	port       string
	subdomains bool
}

func newScope(start *url.URL, subdomains bool) scope {
	return scope{host: bareHost(start), port: start.Port(), subdomains: subdomains}
}

func bareHost(value *url.URL) string {
	return strings.TrimPrefix(strings.ToLower(value.Hostname()), "www.")
}

func (s scope) contains(value *url.URL) bool {
	host := bareHost(value)
	if value.Port() != s.port {
		return false
	}
	return host == s.host || s.subdomains && strings.HasSuffix(host, "."+s.host)
}

func (s scope) normalize(raw, base string) string {
	parent, err := url.Parse(base)
	if err != nil {
		return ""
	}
	reference, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	current := parent.ResolveReference(reference)
	if current.User != nil || current.Scheme != "http" && current.Scheme != "https" || !s.contains(current) {
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
	case ".jpg", ".jpeg", ".png", ".gif", ".svg", ".webp", ".ico", ".css", ".js", ".zip", ".mp4", ".woff", ".woff2":
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

type sitemapEntry struct {
	Location     string `xml:"loc"`
	LastModified string `xml:"lastmod"`
}

type sitemapDocument struct {
	XMLName  xml.Name
	URLs     []sitemapEntry `xml:"url"`
	Sitemaps []sitemapEntry `xml:"sitemap"`
}

func parseSitemap(body string) (sitemapDocument, bool) {
	var document sitemapDocument
	if xml.Unmarshal([]byte(body), &document) != nil {
		return sitemapDocument{}, false
	}
	switch document.XMLName.Local {
	case "urlset", "sitemapindex":
		return document, true
	}
	return sitemapDocument{}, false
}

func robotsSitemaps(body string) []string {
	result := []string{}
	for _, line := range strings.Split(body, "\n") {
		if comment := strings.Index(line, "#"); comment >= 0 {
			line = line[:comment]
		}
		line = strings.TrimSpace(line)
		if len(line) > len("sitemap:") && strings.EqualFold(line[:len("sitemap:")], "sitemap:") {
			if value := strings.TrimSpace(line[len("sitemap:"):]); value != "" {
				result = append(result, value)
			}
		}
		if len(result) >= 10 {
			break
		}
	}
	return result
}

func pageLinks(rawHTML string) []string {
	dom, err := goquery.NewDocumentFromReader(strings.NewReader(rawHTML))
	if err != nil {
		return nil
	}
	result := []string{}
	dom.Find("a[href]").EachWithBreak(func(_ int, selection *goquery.Selection) bool {
		href, _ := selection.Attr("href")
		result = append(result, href)
		return len(result) < maximumPageLinks
	})
	return result
}

type globs []*regexp.Regexp

func compileGlobs(patterns []string) globs {
	result := make(globs, 0, len(patterns))
	for _, pattern := range patterns {
		parts := strings.Split(pattern, "*")
		for i, part := range parts {
			parts[i] = regexp.QuoteMeta(part)
		}
		result = append(result, regexp.MustCompile("^"+strings.Join(parts, ".*")+"$"))
	}
	return result
}

func (g globs) match(value string) bool {
	for _, expression := range g {
		if expression.MatchString(value) {
			return true
		}
	}
	return false
}

func templatesOf(entries []Entry) []urlshape.Template {
	if len(entries) == 0 {
		return nil
	}
	urls := make([]string, len(entries))
	for i, entry := range entries {
		urls[i] = entry.URL
	}
	return urlshape.Templates(urls, templateThreshold)
}

const maximumRobotsRules = 200

type robotsRule struct {
	allow   bool
	length  int
	pattern *regexp.Regexp
}

type robotsRules []robotsRule

func parseRobotsRules(body string) robotsRules {
	var rules robotsRules
	applies, afterAgent := false, false
	for _, line := range strings.Split(body, "\n") {
		if comment := strings.Index(line, "#"); comment >= 0 {
			line = line[:comment]
		}
		name, value, found := strings.Cut(strings.TrimSpace(line), ":")
		if !found {
			continue
		}
		name, value = strings.ToLower(strings.TrimSpace(name)), strings.TrimSpace(value)
		switch name {
		case "user-agent":
			applies = applies && afterAgent || value == "*"
			afterAgent = true
		case "allow", "disallow":
			afterAgent = false
			if !applies || value == "" || len(rules) >= maximumRobotsRules {
				continue
			}
			expression := "^" + strings.ReplaceAll(regexp.QuoteMeta(value), `\*`, ".*")
			if strings.HasSuffix(expression, `\$`) {
				expression = strings.TrimSuffix(expression, `\$`) + "$"
			}
			rules = append(rules, robotsRule{allow: name == "allow", length: len(value), pattern: regexp.MustCompile(expression)})
		}
	}
	return rules
}

func (r robotsRules) blocks(rawURL string) bool {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	target := parsed.EscapedPath()
	if parsed.RawQuery != "" {
		target += "?" + parsed.RawQuery
	}
	best, blocked := -1, false
	for _, rule := range r {
		if rule.pattern.MatchString(target) && (rule.length > best || rule.length == best && rule.allow) {
			best, blocked = rule.length, !rule.allow
		}
	}
	return blocked
}
