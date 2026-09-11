package extract

import (
	"strings"
	"testing"
)

func TestHTML(t *testing.T) {
	t.Parallel()

	raw := `<html><head><title>Example</title><meta name="description" content="A useful page"></head><body><nav>Skip me</nav><main><h1>Example heading</h1><p>` + strings.Repeat("Readable article sentence. ", 20) + `</p><a href="/about">About</a><a href="https://other.example/path">Other</a></main></body></html>`
	result, err := HTML(raw, "https://example.com/page")
	if err != nil {
		t.Fatalf("HTML() error = %v", err)
	}
	if !strings.Contains(result.Content, "Example heading") {
		t.Errorf("HTML() content does not contain heading: %q", result.Content)
	}
	if strings.Contains(result.Content, "Skip me") {
		t.Errorf("HTML() content retained navigation: %q", result.Content)
	}
	if len(result.Links) != 1 || result.Links[0] != "https://example.com/about" {
		t.Errorf("HTML() links = %v, want same-site about link", result.Links)
	}
}
