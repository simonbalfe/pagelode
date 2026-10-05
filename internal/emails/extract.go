package emails

import (
	"encoding/hex"
	"encoding/json"
	"net/mail"
	"net/url"
	"regexp"
	"sort"
	"strings"

	"github.com/PuerkitoBio/goquery"
	"github.com/simonbalfe/pagelode/internal/page"
	"golang.org/x/net/html"
)

var addressPattern = regexp.MustCompile("[a-zA-Z0-9.!#$%&'*+/=?^_`{|}~-]+@[a-zA-Z0-9](?:[a-zA-Z0-9-]*[a-zA-Z0-9])?(?:\\.[a-zA-Z0-9](?:[a-zA-Z0-9-]*[a-zA-Z0-9])?)+")
var obfuscation = strings.NewReplacer("[at]", "@", "(at)", "@", "[dot]", ".", "(dot)", ".")
var spacedObfuscation = regexp.MustCompile(`\s*(\[at\]|\(at\)|\[dot\]|\(dot\))\s*`)

type match struct {
	address string
	kind    string
	source  string
}

func extractAddresses(document page.Document) []match {
	found := map[match]bool{}
	source := document.FinalURL
	add := func(text, kind string) {
		for _, candidate := range addressPattern.FindAllString(text, 2000) {
			address := strings.ToLower(strings.Trim(candidate, "."))
			if len(address) > 254 {
				continue
			}
			parsed, err := mail.ParseAddress(address)
			if err == nil && parsed.Address == address {
				found[match{address, kind, source}] = true
			}
		}
	}
	dom, err := goquery.NewDocumentFromReader(strings.NewReader(document.HTML))
	if err == nil {
		dom.Find("a[href]").Each(func(_ int, selection *goquery.Selection) {
			href, _ := selection.Attr("href")
			if strings.HasPrefix(strings.ToLower(href), "mailto:") {
				recipients, err := url.PathUnescape(strings.SplitN(href[len("mailto:"):], "?", 2)[0])
				if err == nil {
					add(recipients, "mailto")
				}
			}
			if strings.Contains(href, "/cdn-cgi/l/email-protection#") {
				add(decodeCloudflare(strings.SplitN(href, "#", 2)[1]), "decoded")
			}
		})
		dom.Find("[data-cfemail]").Each(func(_ int, selection *goquery.Selection) {
			encoded, _ := selection.Attr("data-cfemail")
			add(decodeCloudflare(encoded), "decoded")
		})
		dom.Find(`script[type="application/ld+json"],script[type="application/json"],script#__NEXT_DATA__`).Each(func(_ int, selection *goquery.Selection) {
			extractJSON(selection.Text(), func(text string) { add(text, "structured_data") })
		})
		dom.Find("script,style,noscript,template,svg,[hidden],[aria-hidden=true]").Remove()
		body := dom.Find("body").First()
		if len(body.Nodes) > 0 {
			var text strings.Builder
			visibleText(body.Nodes[0], &text)
			add(text.String(), "text")
			decoded := obfuscation.Replace(spacedObfuscation.ReplaceAllString(text.String(), "$1"))
			if decoded != text.String() {
				add(decoded, "decoded")
			}
		}
	}
	extractJSON(document.Text, func(text string) { add(text, "structured_data") })
	if !json.Valid([]byte(document.Text)) {
		add(document.Text, "text")
	}
	if document.Traffic != nil {
		for _, entry := range document.Traffic.Entries {
			if entry.Status >= 200 && entry.Status < 300 {
				source = entry.URL
				extractJSON(entry.ResponseBody, func(text string) { add(text, "network_json") })
			}
		}
	}
	result := make([]match, 0, len(found))
	for item := range found {
		result = append(result, item)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].address == result[j].address {
			return result[i].kind < result[j].kind
		}
		return result[i].address < result[j].address
	})
	return result
}

func visibleText(node *html.Node, text *strings.Builder) {
	if node.Type == html.TextNode {
		text.WriteString(node.Data)
		return
	}
	block := strings.Contains(" body p div section article header footer li td th h1 h2 h3 h4 h5 h6 br ", " "+node.Data+" ")
	if block {
		text.WriteByte(' ')
	}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		visibleText(child, text)
	}
	if block {
		text.WriteByte(' ')
	}
}

func extractJSON(body string, add func(string)) {
	var value any
	if json.Unmarshal([]byte(body), &value) != nil {
		return
	}
	walkJSON(value, false, 0, add)
}

func walkJSON(value any, emailField bool, depth int, add func(string)) {
	if depth > 16 {
		return
	}
	switch value := value.(type) {
	case map[string]any:
		for name, child := range value {
			walkJSON(child, emailField || strings.Contains(strings.ToLower(name), "email"), depth+1, add)
		}
	case []any:
		for _, child := range value {
			walkJSON(child, emailField, depth+1, add)
		}
	case string:
		if emailField {
			add(value)
		}
	}
}

func decodeCloudflare(encoded string) string {
	data, err := hex.DecodeString(encoded)
	if err != nil || len(data) < 2 {
		return ""
	}
	for i := 1; i < len(data); i++ {
		data[i] ^= data[0]
	}
	return string(data[1:])
}
