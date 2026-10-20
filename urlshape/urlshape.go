// Package urlshape groups URLs by structure: Shape and Sampler cap how many
// URLs of one shape a crawl follows, and Templates summarises URL lists as
// wildcard patterns. The approach is inspired by Katana (see
// licenses/katana.txt).
package urlshape

import (
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
)

const (
	idMarker       = "#"
	wildcard       = "*"
	templateSample = 3
	minHexLength   = 8
)

// Shape returns a key combining the host, first path segment (with ID-like
// segments masked), segment count, and query parameter names of rawURL.
// Unparseable input is returned unchanged.
func Shape(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	segments := pathSegments(u.Path)
	first := ""
	for i, segment := range segments {
		if looksLikeID(segment) {
			segments[i] = idMarker
		}
	}
	if len(segments) > 0 {
		first = segments[0]
	}

	var b strings.Builder
	b.WriteString(strings.ToLower(u.Host))
	b.WriteByte('|')
	b.WriteString(first)
	b.WriteByte('|')
	b.WriteString(strconv.Itoa(len(segments)))
	b.WriteByte('|')
	b.WriteString(strings.Join(queryNames(u), ","))
	return b.String()
}

// Sampler admits a bounded number of URLs per Shape. It is safe for concurrent use.
type Sampler struct {
	mu     sync.Mutex
	limit  int
	counts map[string]int
}

// NewSampler returns a Sampler allowing limit URLs per shape; limit <= 0 allows all.
func NewSampler(limit int) *Sampler {
	return &Sampler{limit: limit, counts: make(map[string]int)}
}

// Allow reports whether rawURL may be followed, counting it against its shape when it may.
func (s *Sampler) Allow(rawURL string) bool {
	if s.limit <= 0 {
		return true
	}
	shape := Shape(rawURL)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.counts[shape] >= s.limit {
		return false
	}
	s.counts[shape]++
	return true
}

// Template is a URL pattern with "*" for ID-like or high-variety path
// segments, the number of URLs matching it, and up to three sample URLs.
type Template struct {
	Pattern string   `json:"pattern"`
	Count   int      `json:"count"`
	Samples []string `json:"samples"`
}

type trieNode struct {
	children map[string]*trieNode
	wild     bool
}

func newTrieNode() *trieNode {
	return &trieNode{children: make(map[string]*trieNode)}
}

func (n *trieNode) absorb(other *trieNode) {
	for key, child := range other.children {
		if existing, ok := n.children[key]; ok {
			existing.absorb(child)
			continue
		}
		n.children[key] = child
	}
}

func (n *trieNode) collapse(threshold int, root bool) {
	if !root && threshold > 0 && len(n.children) >= threshold {
		merged := newTrieNode()
		for _, child := range n.children {
			merged.absorb(child)
		}
		n.children = map[string]*trieNode{wildcard: merged}
		n.wild = true
	}
	for _, child := range n.children {
		child.collapse(threshold, false)
	}
}

type parsedURL struct {
	raw      string
	host     string
	segments []string
	query    string
}

// Templates groups urls into patterns per host, collapsing any non-root path
// position with at least threshold distinct children into "*" (threshold <= 0
// disables collapsing). Results are sorted by descending count, then pattern.
func Templates(urls []string, threshold int) []Template {
	parsed := make([]parsedURL, 0, len(urls))
	roots := make(map[string]*trieNode)
	for _, raw := range urls {
		u, err := url.Parse(raw)
		if err != nil || raw == "" {
			continue
		}
		segments := pathSegments(u.Path)
		for i, segment := range segments {
			if looksLikeID(segment) {
				segments[i] = wildcard
			}
		}
		host := strings.ToLower(u.Host)
		root, ok := roots[host]
		if !ok {
			root = newTrieNode()
			roots[host] = root
		}
		node := root
		for _, segment := range segments {
			child, ok := node.children[segment]
			if !ok {
				child = newTrieNode()
				node.children[segment] = child
			}
			node = child
		}
		parsed = append(parsed, parsedURL{raw: raw, host: host, segments: segments, query: strings.Join(queryNames(u), "&")})
	}
	for _, root := range roots {
		root.collapse(threshold, true)
	}

	byPattern := make(map[string]*Template)
	for _, p := range parsed {
		pattern := templatePattern(roots[p.host], p.segments, p.query)
		t, ok := byPattern[pattern]
		if !ok {
			t = &Template{Pattern: pattern, Samples: []string{}}
			byPattern[pattern] = t
		}
		t.Count++
		if len(t.Samples) < templateSample && !contains(t.Samples, p.raw) {
			t.Samples = append(t.Samples, p.raw)
		}
	}

	templates := make([]Template, 0, len(byPattern))
	for _, t := range byPattern {
		templates = append(templates, *t)
	}
	sort.Slice(templates, func(i, j int) bool {
		if templates[i].Count != templates[j].Count {
			return templates[i].Count > templates[j].Count
		}
		return templates[i].Pattern < templates[j].Pattern
	})
	return templates
}

func templatePattern(root *trieNode, segments []string, query string) string {
	parts := make([]string, len(segments))
	node := root
	for i, segment := range segments {
		if node.wild {
			segment = wildcard
		}
		parts[i] = segment
		node = node.children[segment]
	}
	pattern := "/" + strings.Join(parts, "/")
	if query != "" {
		pattern += "?" + query
	}
	return pattern
}

func pathSegments(path string) []string {
	segments := make([]string, 0, 4)
	for _, segment := range strings.Split(path, "/") {
		if segment != "" {
			segments = append(segments, segment)
		}
	}
	return segments
}

func queryNames(u *url.URL) []string {
	values := u.Query()
	if len(values) == 0 {
		return nil
	}
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func looksLikeID(segment string) bool {
	if segment == "" {
		return false
	}
	if allMatch(segment, isDigit) {
		return true
	}
	if len(segment) == 36 && isUUID(segment) {
		return true
	}
	return len(segment) >= minHexLength && allMatch(segment, isHex)
}

func isUUID(s string) bool {
	for i := range len(s) {
		switch i {
		case 8, 13, 18, 23:
			if s[i] != '-' {
				return false
			}
		default:
			if !isHex(s[i]) {
				return false
			}
		}
	}
	return true
}

func allMatch(s string, pred func(byte) bool) bool {
	for i := range len(s) {
		if !pred(s[i]) {
			return false
		}
	}
	return true
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func isHex(c byte) bool {
	return isDigit(c) || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}
