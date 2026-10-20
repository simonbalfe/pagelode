package browser

import (
	"net/url"
	"regexp"
	"strings"
)

var (
	placeholders    = []string{"{session}", "%7Bsession%7D", "%7bsession%7d"}
	passwordSession = regexp.MustCompile(`(_(?:hard|locked)?session-)[A-Za-z0-9]+`)
)

func StickyProxy(raw string) bool {
	for _, placeholder := range placeholders {
		if strings.Contains(raw, placeholder) {
			return true
		}
	}
	password, ok := urlPassword(raw)
	return ok && passwordSession.MatchString(password)
}

func ProxyWithSession(raw string, id string) string {
	for _, placeholder := range placeholders {
		if strings.Contains(raw, placeholder) {
			return replacePlaceholders(raw, id)
		}
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.User == nil {
		return raw
	}
	password, ok := parsed.User.Password()
	if !ok || !passwordSession.MatchString(password) {
		return raw
	}
	parsed.User = url.UserPassword(parsed.User.Username(), passwordSession.ReplaceAllString(password, "${1}"+id))
	return parsed.String()
}

func replacePlaceholders(raw string, id string) string {
	for _, placeholder := range placeholders {
		raw = strings.ReplaceAll(raw, placeholder, id)
	}
	return raw
}

func urlPassword(raw string) (string, bool) {
	parsed, err := url.Parse(replacePlaceholders(raw, "x"))
	if err != nil || parsed.User == nil {
		return "", false
	}
	return parsed.User.Password()
}
