// Package similarity detects near-duplicate pages using SimHash fingerprints
// of their visible text. The approach is inspired by Katana (see
// licenses/katana.txt).
package similarity

import (
	"hash/fnv"
	"math/bits"
	"strings"
	"sync"
	"unicode"

	"github.com/PuerkitoBio/goquery"
)

const shingleSize = 3

const blockElements = "title, p, div, br, li, tr, td, th, h1, h2, h3, h4, h5, h6, section, article, header, footer, nav, main, aside, blockquote, pre, ul, ol, dl, dt, dd, table, form, hr"

// Text returns the visible text of html with whitespace collapsed, excluding
// scripts, styles, and similar non-content elements. It returns "" when the
// HTML cannot be parsed.
func Text(html string) string {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return ""
	}
	doc.Find("script, style, noscript, svg, template").Remove()
	doc.Find(blockElements).AfterHtml(" ")
	return strings.Join(strings.Fields(doc.Text()), " ")
}

// Fingerprint returns a 64-bit SimHash of text built from lowercased word
// shingles. Empty text fingerprints to 0.
func Fingerprint(text string) uint64 {
	words := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	})
	if len(words) == 0 {
		return 0
	}

	var weights [64]int
	add := func(feature string) {
		h := mix(hashString(feature))
		for bit := range 64 {
			if h&(1<<bit) != 0 {
				weights[bit]++
			} else {
				weights[bit]--
			}
		}
	}
	if len(words) < shingleSize {
		for _, word := range words {
			add(word)
		}
	} else {
		for i := 0; i+shingleSize <= len(words); i++ {
			add(strings.Join(words[i:i+shingleSize], " "))
		}
	}

	var fingerprint uint64
	for bit, weight := range weights {
		if weight > 0 {
			fingerprint |= 1 << bit
		}
	}
	return fingerprint
}

// Distance returns the Hamming distance between two fingerprints.
func Distance(a, b uint64) int {
	return bits.OnesCount64(a ^ b)
}

// Index remembers fingerprints and reports near-duplicates. It is safe for
// concurrent use.
type Index struct {
	mu           sync.Mutex
	maxDistance  int
	fingerprints []uint64
}

// NewIndex returns an Index that treats fingerprints within maxDistance bits as duplicates.
func NewIndex(maxDistance int) *Index {
	return &Index{maxDistance: maxDistance}
}

// Seen reports whether fingerprint is within the index's distance of a known
// fingerprint; otherwise it records fingerprint and returns false.
func (i *Index) Seen(fingerprint uint64) bool {
	i.mu.Lock()
	defer i.mu.Unlock()
	for _, known := range i.fingerprints {
		if Distance(known, fingerprint) <= i.maxDistance {
			return true
		}
	}
	i.fingerprints = append(i.fingerprints, fingerprint)
	return false
}

func hashString(s string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(s))
	return h.Sum64()
}

func mix(x uint64) uint64 {
	x ^= x >> 30
	x *= 0xbf58476d1ce4e5b9
	x ^= x >> 27
	x *= 0x94d049bb133111eb
	x ^= x >> 31
	return x
}
