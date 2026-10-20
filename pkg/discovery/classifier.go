package discovery

import (
	"encoding/json"
	"net"
	"net/url"
	"regexp"
	"strings"

	"github.com/simonbalfe/pagelode/pkg/page"
)

var (
	uuidSegmentPattern = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	hashSegmentPattern = regexp.MustCompile(`(?i)^[0-9a-f]{32,}$`)
	numericPattern     = regexp.MustCompile(`^\d+$`)
	soapEnvelopeStart  = regexp.MustCompile(`(?i)<(?:[a-z0-9_.-]+:)?envelope[\s>]`)
)

const maxEnvelopeProbe = 4096

var restPathSegments = []string{"/api/", "/v1/", "/v2/", "/v3/", "/rest/", "/rpc/", "/jsonrpc", "/.well-known/", "/oauth/", "/openapi"}

func defaultBlocklist() []string {
	return []string{
		"google-analytics.com",
		"doubleclick.net",
		"sentry.io",
		"facebook.com",
		"googlesyndication.com",
		"googletagmanager.com",
		"fonts.googleapis.com",
		"gstatic.com",
		"bat.bing.com",
		"criteo.com",
		"demdex.net",
		"onetrust.com",
		"cookielaw.org",
		"amazon-adsystem.com",
		"adsymptotic.com",
		"improving.duckduckgo.com",
		"lngtd.com",
		"kargo.com",
		"segment.io",
		"api.segment.io",
		"mixpanel.com",
		"amplitude.com",
		"hotjar.com",
		"newrelic.com",
		"fullstory.com",
		"intercom.io",
		"branch.io",
		"stats.g.doubleclick.net",
		"adservice.google.com",
		"connect.facebook.net",
	}
}

func scoreEntry(entry page.Exchange, blocklist []string) int {
	score := 0
	responseType := strings.ToLower(entry.MIMEType)
	requestType := strings.ToLower(getHeaderValue(entry.RequestHeaders, "Content-Type"))
	path := strings.ToLower(extractPath(entry.URL))
	host := strings.ToLower(extractHost(entry.URL))
	urlLower := strings.ToLower(entry.URL)

	if strings.Contains(responseType, "application/json") {
		score += 2
	}

	if strings.Contains(requestType, "application/json") || strings.Contains(requestType, "application/x-www-form-urlencoded") {
		score++
	}

	for _, indicator := range []string{"/api/", "/v1/", "/v2/", "/v3/", "/graphql", "/data/", "/youtubei/"} {
		if strings.Contains(path, indicator) {
			score++
			break
		}
	}

	if isValidJSONBody(entry.ResponseBody) {
		score++
	}

	score += structuredAPIBonus(entry, requestType, responseType, path)

	if hostMatchesBlocklist(host, blocklist) {
		score -= 3
	}

	for _, prefix := range []string{"image/", "text/css", "text/html", "application/javascript", "font/"} {
		if strings.HasPrefix(responseType, prefix) {
			score -= 2
			break
		}
	}

	for _, suffix := range []string{".js", ".css", ".png", ".jpg", ".woff", ".svg", ".ico"} {
		if strings.HasSuffix(urlLower, suffix) {
			score--
			break
		}
	}

	return score
}

func structuredAPIBonus(entry page.Exchange, requestType, responseType, path string) int {
	if isSOAP(entry, requestType, responseType) ||
		strings.Contains(requestType, "application/grpc") || strings.Contains(responseType, "application/grpc") ||
		strings.Contains(requestType, "application/graphql") || strings.Contains(responseType, "application/graphql") {
		return 2
	}
	mutating := isMutatingMethod(entry.Method)
	if strings.Contains(requestType, "application/xml") || strings.Contains(responseType, "application/xml") {
		if mutating || matchesRESTPath(path) {
			return 2
		}
	}
	if mutating && strings.Contains(requestType, "multipart/form-data") {
		return 1
	}
	return 0
}

func isSOAP(entry page.Exchange, requestType, responseType string) bool {
	if hasHeader(entry.RequestHeaders, "SOAPAction") ||
		strings.Contains(requestType, "soap+xml") || strings.Contains(responseType, "soap+xml") {
		return true
	}
	if !strings.Contains(requestType, "xml") && !strings.Contains(responseType, "xml") {
		return false
	}
	return hasSOAPEnvelope(entry.RequestBody) || hasSOAPEnvelope(entry.ResponseBody)
}

func hasSOAPEnvelope(body string) bool {
	if len(body) > maxEnvelopeProbe {
		body = body[:maxEnvelopeProbe]
	}
	return soapEnvelopeStart.MatchString(body) && strings.Contains(strings.ToLower(body), "soap")
}

func isMutatingMethod(method string) bool {
	switch strings.ToUpper(strings.TrimSpace(method)) {
	case "POST", "PUT", "PATCH", "DELETE":
		return true
	}
	return false
}

func matchesRESTPath(path string) bool {
	for _, segment := range restPathSegments {
		if strings.Contains(path, segment) {
			return true
		}
	}
	return false
}

func hasHeader(headers map[string]string, want string) bool {
	for key := range headers {
		if strings.EqualFold(key, want) {
			return true
		}
	}
	return false
}

func getHeaderValue(headers map[string]string, want string) string {
	for key, value := range headers {
		if strings.EqualFold(key, want) {
			return value
		}
	}

	return ""
}

func isValidJSONBody(body string) bool {
	if strings.TrimSpace(body) == "" {
		return false
	}

	var payload any
	return json.Unmarshal([]byte(body), &payload) == nil
}

func hostMatchesBlocklist(host string, blocklist []string) bool {
	if host == "" {
		return false
	}

	for _, blocked := range blocklist {
		blocked = strings.ToLower(blocked)
		if host == blocked || strings.HasSuffix(host, "."+blocked) {
			return true
		}
	}

	return false
}

func extractHost(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err == nil && parsed.Host != "" {
		return parsed.Hostname()
	}

	host := rawURL
	if idx := strings.Index(host, "://"); idx >= 0 {
		host = host[idx+3:]
	}
	if idx := strings.IndexAny(host, "/?"); idx >= 0 {
		host = host[:idx]
	}

	host, _, err = net.SplitHostPort(host)
	if err == nil {
		return host
	}

	return host
}

func extractPath(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err == nil && parsed.Path != "" {
		return parsed.Path
	}

	path := rawURL
	if idx := strings.Index(path, "://"); idx >= 0 {
		path = path[idx+3:]
		if slash := strings.Index(path, "/"); slash >= 0 {
			path = path[slash:]
		} else {
			return "/"
		}
	}
	if idx := strings.Index(path, "?"); idx >= 0 {
		path = path[:idx]
	}
	if path == "" {
		return "/"
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}

	return path
}

func normalizeEntryPath(rawURL string) string {
	path := extractPath(rawURL)
	segments := strings.Split(path, "/")
	for i, segment := range segments {
		switch {
		case numericPattern.MatchString(segment):
			segments[i] = "{id}"
		case uuidSegmentPattern.MatchString(segment):
			segments[i] = "{uuid}"
		case hashSegmentPattern.MatchString(segment):
			segments[i] = "{hash}"
		}
	}

	normalized := strings.Join(segments, "/")
	if normalized == "" {
		return "/"
	}

	return normalized
}
