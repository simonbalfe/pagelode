package extract

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"

	"codeberg.org/readeck/go-readability/v2"
	"github.com/JohannesKaufmann/html-to-markdown"
	"github.com/PuerkitoBio/goquery"
)

const (
	minimumArticleCharacters = 250
	maximumLinks             = 100
	maximumStructuredChars   = 2_500
)

type Result struct {
	Content string
	Links   []string
	Title   string
}

func HTML(rawHTML string, baseURL string) (Result, error) {
	parsedURL, err := url.Parse(baseURL)
	if err != nil {
		return Result{}, fmt.Errorf("extract: parse base URL: %w", err)
	}
	document, err := goquery.NewDocumentFromReader(strings.NewReader(rawHTML))
	if err != nil {
		return Result{}, fmt.Errorf("extract: parse HTML: %w", err)
	}

	title := normalized(document.Find("title").First().Text())
	body := readabilityMarkdown(rawHTML, parsedURL)
	if body == "" {
		body, err = fallbackMarkdown(document, baseURL)
		if err != nil {
			return Result{}, err
		}
	}
	structured := structuredData(document)
	content := body
	if structured != "" {
		content = structured
		if body != "" {
			content += "\n\n" + body
		}
	}

	return Result{
		Content: tidy(content),
		Links:   sameSiteLinks(document, parsedURL),
		Title:   title,
	}, nil
}

func readabilityMarkdown(rawHTML string, parsedURL *url.URL) string {
	article, err := readability.FromReader(strings.NewReader(rawHTML), parsedURL)
	if err != nil || article.Node == nil {
		return ""
	}
	var text bytes.Buffer
	if err := article.RenderText(&text); err != nil || len([]rune(normalized(text.String()))) < minimumArticleCharacters {
		return ""
	}
	var readableHTML bytes.Buffer
	if err := article.RenderHTML(&readableHTML); err != nil {
		return ""
	}
	markdown, err := md.NewConverter(parsedURL.String(), true, nil).ConvertString(readableHTML.String())
	if err != nil {
		return ""
	}
	return tidy(markdown)
}

func fallbackMarkdown(document *goquery.Document, baseURL string) (string, error) {
	selection := document.Find("body").First().Clone()
	if selection.Length() == 0 {
		return "", nil
	}
	selection.Find("nav,footer,header,aside,script,style,form,iframe,noscript,svg,link,meta,img").Remove()
	selection.Find("[hidden],[aria-hidden=true]").Remove()
	selection.Find(".cookie,.cookies,.advert,.advertisement,.social-share,.share-buttons").Remove()

	html, err := selection.Html()
	if err != nil {
		return "", fmt.Errorf("extract: serialize cleaned HTML: %w", err)
	}
	markdown, err := md.NewConverter(baseURL, true, nil).ConvertString(html)
	if err != nil {
		return "", fmt.Errorf("extract: convert Markdown: %w", err)
	}
	return tidy(markdown), nil
}

func sameSiteLinks(document *goquery.Document, baseURL *url.URL) []string {
	links := make(map[string]struct{})
	document.Find("a[href]").EachWithBreak(func(_ int, selection *goquery.Selection) bool {
		href, ok := selection.Attr("href")
		if !ok {
			return true
		}
		parsed, err := url.Parse(href)
		if err != nil {
			return true
		}
		absolute := baseURL.ResolveReference(parsed)
		absolute.Fragment = ""
		if (absolute.Scheme == "http" || absolute.Scheme == "https") && strings.EqualFold(absolute.Host, baseURL.Host) {
			links[absolute.String()] = struct{}{}
		}
		return len(links) < maximumLinks
	})

	result := make([]string, 0, len(links))
	for link := range links {
		result = append(result, link)
	}
	sort.Strings(result)
	return result
}

func structuredData(document *goquery.Document) string {
	lines := make(map[string]struct{})
	document.Find(`script[type="application/ld+json"]`).Each(func(_ int, selection *goquery.Selection) {
		var value any
		if err := json.Unmarshal([]byte(selection.Text()), &value); err != nil {
			return
		}
		collectStructured(value, lines)
	})
	if description, ok := document.Find(`meta[name="description"],meta[property="og:description"]`).First().Attr("content"); ok {
		if value := normalized(description); value != "" {
			lines["Description: "+value] = struct{}{}
		}
	}
	if len(lines) == 0 {
		return ""
	}

	ordered := make([]string, 0, len(lines))
	for line := range lines {
		ordered = append(ordered, line)
	}
	sort.Strings(ordered)
	content := strings.Join(ordered, "\n")
	if len(content) > maximumStructuredChars {
		content = content[:maximumStructuredChars] + "…"
	}
	return "## Page structured data\n" + content
}

func collectStructured(value any, lines map[string]struct{}) {
	switch typed := value.(type) {
	case []any:
		for _, item := range typed {
			collectStructured(item, lines)
		}
	case map[string]any:
		if graph, ok := typed["@graph"]; ok {
			collectStructured(graph, lines)
		}
		renderStructuredNode(typed, lines)
	}
}

func renderStructuredNode(node map[string]any, lines map[string]struct{}) {
	types := stringValues(node["@type"])
	for _, nodeType := range types {
		switch strings.ToLower(nodeType) {
		case "organization", "corporation", "localbusiness", "newsmediaorganization":
			facts := compactFacts(node, []string{"name", "foundingDate", "url"})
			if len(facts) > 0 {
				lines["Organization: "+strings.Join(facts, "; ")] = struct{}{}
			}
		case "product":
			facts := compactFacts(node, []string{"name", "sku", "description"})
			if len(facts) > 0 {
				lines["Product: "+strings.Join(facts, "; ")] = struct{}{}
			}
		case "faqpage":
			for _, question := range mapValues(node["mainEntity"]) {
				name := scalar(question["name"])
				answer := ""
				if accepted, ok := question["acceptedAnswer"].(map[string]any); ok {
					answer = scalar(accepted["text"])
				}
				if name != "" && answer != "" {
					lines["FAQ: "+name+" "+answer] = struct{}{}
				}
			}
		}
	}
}

func compactFacts(node map[string]any, keys []string) []string {
	result := make([]string, 0, len(keys))
	for _, key := range keys {
		if value := scalar(node[key]); value != "" {
			result = append(result, key+": "+value)
		}
	}
	return result
}

func stringValues(value any) []string {
	switch typed := value.(type) {
	case string:
		return []string{typed}
	case []any:
		result := make([]string, 0, len(typed))
		for _, item := range typed {
			if text, ok := item.(string); ok {
				result = append(result, text)
			}
		}
		return result
	default:
		return nil
	}
}

func mapValues(value any) []map[string]any {
	switch typed := value.(type) {
	case map[string]any:
		return []map[string]any{typed}
	case []any:
		result := make([]map[string]any, 0, len(typed))
		for _, item := range typed {
			if mapped, ok := item.(map[string]any); ok {
				result = append(result, mapped)
			}
		}
		return result
	default:
		return nil
	}
}

func scalar(value any) string {
	switch typed := value.(type) {
	case string:
		return normalized(strings.NewReplacer("<", " ", ">", " ").Replace(typed))
	case float64:
		return fmt.Sprintf("%g", typed)
	default:
		return ""
	}
}

func normalized(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func tidy(value string) string {
	lines := strings.Split(strings.ReplaceAll(value, "\r\n", "\n"), "\n")
	result := make([]string, 0, len(lines))
	empty := false
	for _, line := range lines {
		trimmed := strings.TrimRight(line, " \t")
		if strings.TrimSpace(trimmed) == "" {
			if empty {
				continue
			}
			empty = true
			result = append(result, "")
			continue
		}
		empty = false
		result = append(result, trimmed)
	}
	return strings.TrimSpace(strings.Join(result, "\n"))
}
