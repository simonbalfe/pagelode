package sitemap

import (
	"net/url"
	"regexp"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

const (
	maxEndpoints   = 500
	maxEndpointLen = 200
	maxScripts     = 20
)

var quoted = regexp.MustCompile("\"(/[^\"\\n\\r]*)\"|'(/[^'\\n\\r]*)'|`(/[^`\\n\\r]*)`")

var mimeLike = regexp.MustCompile(`(?i)^/(application|text|image|audio|video|font|multipart)/(json|xml|html|plain|javascript|css|csv|pdf|png|jpeg|gif|webp|svg\+xml|octet-stream|x-www-form-urlencoded|form-data|zip)$`)

var staticExtensions = map[string]struct{}{
	"png": {}, "jpg": {}, "jpeg": {}, "gif": {}, "svg": {}, "webp": {}, "ico": {},
	"css": {}, "woff": {}, "woff2": {}, "ttf": {}, "map": {}, "mp4": {},
}

func ScriptEndpoints(source string) []string {
	var endpoints []string
	seen := make(map[string]struct{})
	for _, match := range quoted.FindAllStringSubmatch(source, -1) {
		value := match[1] + match[2] + match[3]
		if !keepEndpoint(value) {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		endpoints = append(endpoints, value)
		if len(endpoints) == maxEndpoints {
			break
		}
	}
	return endpoints
}

func keepEndpoint(value string) bool {
	if value == "/" || len(value) > maxEndpointLen || strings.HasPrefix(value, "//") {
		return false
	}
	if strings.ContainsAny(value, "{}\\") || strings.ContainsFunc(value, isSpace) {
		return false
	}
	if mimeLike.MatchString(value) {
		return false
	}
	return !staticAsset(value)
}

func isSpace(r rune) bool {
	return r == ' ' || r == '\t' || r == '\v' || r == '\f' || r == 0xA0
}

func staticAsset(value string) bool {
	path := value
	if i := strings.IndexAny(path, "?#"); i >= 0 {
		path = path[:i]
	}
	dot := strings.LastIndexByte(path, '.')
	if dot < 0 || dot < strings.LastIndexByte(path, '/') {
		return false
	}
	_, ok := staticExtensions[strings.ToLower(path[dot+1:])]
	return ok
}

func ScriptURLs(rawHTML string, base *url.URL) []string {
	if base == nil {
		return nil
	}
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(rawHTML))
	if err != nil {
		return nil
	}
	baseHost := siteHost(base)
	if baseHost == "" {
		return nil
	}

	var urls []string
	seen := make(map[string]struct{})
	doc.Find("script[src]").EachWithBreak(func(_ int, s *goquery.Selection) bool {
		src, _ := s.Attr("src")
		src = strings.TrimSpace(src)
		if src == "" {
			return true
		}
		ref, err := url.Parse(src)
		if err != nil {
			return true
		}
		resolved := base.ResolveReference(ref)
		if resolved.Scheme != "http" && resolved.Scheme != "https" {
			return true
		}
		if siteHost(resolved) != baseHost {
			return true
		}
		resolved.Fragment = ""
		out := resolved.String()
		if _, ok := seen[out]; ok {
			return true
		}
		seen[out] = struct{}{}
		urls = append(urls, out)
		return len(urls) < maxScripts
	})
	return urls
}

func siteHost(u *url.URL) string {
	return strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
}
