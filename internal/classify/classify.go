package classify

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/PuerkitoBio/goquery"
	"github.com/simonbalfe/pagelode/internal/page"
	"github.com/simonbalfe/pagelode/signatures"
)

type Kind string

const (
	Accept      Kind = "accept"
	NeedsRender Kind = "needs_render"
	Blocked     Kind = "blocked"
	Retryable   Kind = "retryable"
	Dead        Kind = "dead"
)

type Result struct {
	Kind        Kind
	Reason      string
	RenderScore int
	VisibleText int
}

var (
	patterns           = signatures.MustLoad()
	javascriptRequired = regexp.MustCompile(`(?i)enable javascript|javascript (?:is )?required|requires javascript|javascript to run`)
	domMutation        = regexp.MustCompile(`(?i)(?:appendChild|replaceChildren|insertAdjacentHTML|createElement|document\.write)\s*\(`)
)

func Document(document page.Document) Result {
	if document.StatusCode == 404 || document.StatusCode == 410 {
		return Result{Kind: Dead, Reason: fmt.Sprintf("HTTP %d", document.StatusCode)}
	}
	if reason, ok := challengeSignal(document); ok {
		return Result{Kind: Blocked, Reason: reason}
	}
	if document.Type != page.ContentHTML {
		if document.StatusCode >= 500 {
			return Result{Kind: Retryable, Reason: fmt.Sprintf("HTTP %d", document.StatusCode)}
		}
		return Result{Kind: Accept, Reason: "non-HTML response"}
	}

	html := document.HTML
	for _, candidate := range patterns.Blocks {
		if candidate.Value.MatchString(first(html, 500_000)) {
			return Result{Kind: Blocked, Reason: candidate.Label}
		}
	}
	if document.StatusCode == 401 || document.StatusCode == 403 || document.StatusCode == 429 || document.StatusCode == 503 || document.StatusCode >= 520 && document.StatusCode <= 530 {
		return Result{Kind: Blocked, Reason: fmt.Sprintf("HTTP %d with HTML content", document.StatusCode)}
	}
	if document.StatusCode >= 500 {
		return Result{Kind: Retryable, Reason: fmt.Sprintf("HTTP %d", document.StatusCode)}
	}
	if document.StatusCode >= 400 {
		return Result{Kind: Retryable, Reason: fmt.Sprintf("HTTP %d", document.StatusCode)}
	}
	if strings.TrimSpace(html) == "" {
		return Result{Kind: NeedsRender, Reason: "empty HTML response", RenderScore: 10}
	}

	parsed, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return Result{Kind: Retryable, Reason: "invalid HTML"}
	}
	body := parsed.Find("body").First()
	if body.Length() == 0 {
		return Result{Kind: NeedsRender, Reason: "HTML has no body", RenderScore: 10}
	}

	visibleTree := body.Clone()
	visibleTree.Find("script,style,noscript,svg,template").Remove()
	visible := normalizedText(visibleTree.Text())
	visibleLength := len([]rune(visible))
	signalText := first(html, 30_000)
	if visibleLength < 2_500 {
		for _, candidate := range patterns.ShortBlocks {
			if candidate.Value.MatchString(signalText) {
				return Result{Kind: Blocked, Reason: candidate.Label, VisibleText: visibleLength}
			}
		}
	}

	score := 0
	reasons := make([]string, 0, 4)
	if javascriptRequired.MatchString(parsed.Find("noscript").Text()) {
		score += 10
		reasons = append(reasons, "noscript requires JavaScript")
	}
	if parsed.Find("#__NEXT_DATA__,#__NUXT_DATA__,[data-reactroot],[data-react-root]").Length() > 0 && visibleLength < 1_000 {
		score += 10
		reasons = append(reasons, "hydration marker with little visible content")
	}
	if emptyApplicationRoot(parsed) && visibleLength < 500 {
		score += 10
		reasons = append(reasons, "empty application root")
	}
	scriptCount := parsed.Find("script[src]").Length()
	if scriptCount >= 4 && visibleLength < 500 {
		score += 6
		reasons = append(reasons, fmt.Sprintf("%d scripts with little visible content", scriptCount))
	}
	if domMutation.MatchString(parsed.Find("script:not([src])").Text()) && visibleLength < 500 {
		score += 5
		reasons = append(reasons, "inline DOM mutation")
	}
	semanticCount := parsed.Find("p,h1,h2,h3,h4,h5,h6,article,section,li,td,pre").Length()
	if semanticCount == 0 && scriptCount > 0 {
		score += 5
		reasons = append(reasons, "script shell has no semantic content")
	}
	if visibleLength < 50 {
		score += 5
		reasons = append(reasons, "minimal visible text")
	}

	if score >= 10 {
		return Result{Kind: NeedsRender, Reason: strings.Join(reasons, "; "), RenderScore: score, VisibleText: visibleLength}
	}
	return Result{Kind: Accept, Reason: "usable HTML", RenderScore: score, VisibleText: visibleLength}
}

func challengeSignal(document page.Document) (string, bool) {
	if document.Challenge != nil {
		return fmt.Sprintf("%s challenge remained after browser settling", document.Challenge.Vendor), true
	}
	for _, header := range patterns.ChallengeHeaders {
		if strings.Contains(strings.ToLower(headerValue(document.Headers, header.Name)), header.Value) {
			return header.Label, true
		}
	}
	for _, marker := range patterns.ChallengeURLs {
		if strings.Contains(document.FinalURL, marker) {
			return "challenge URL " + marker, true
		}
	}
	return "", false
}

func headerValue(headers map[string]string, name string) string {
	if value, ok := headers[name]; ok {
		return value
	}
	for key, value := range headers {
		if strings.EqualFold(key, name) {
			return value
		}
	}
	return ""
}

func emptyApplicationRoot(document *goquery.Document) bool {
	empty := false
	document.Find("#app,#root,#__next,#__nuxt").EachWithBreak(func(_ int, selection *goquery.Selection) bool {
		copy := selection.Clone()
		copy.Find("script,style,noscript,svg,template").Remove()
		if normalizedText(copy.Text()) == "" {
			empty = true
			return false
		}
		return true
	})
	return empty
}

func normalizedText(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func first(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit]
}
