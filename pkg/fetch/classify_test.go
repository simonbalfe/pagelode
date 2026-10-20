package fetch

import (
	"strings"
	"testing"
)

func TestDocument(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		doc  Document
		want Kind
	}{
		{
			name: "usable page",
			doc:  Document{StatusCode: 200, Type: ContentHTML, HTML: "<html><body><main><h1>Example</h1><p>" + strings.Repeat("Useful content. ", 20) + "</p></main></body></html>"},
			want: Accept,
		},
		{
			name: "javascript shell",
			doc:  Document{StatusCode: 200, Type: ContentHTML, HTML: `<html><body><div id="root"></div><script src="/1.js"></script><script src="/2.js"></script><script src="/3.js"></script><script src="/4.js"></script></body></html>`},
			want: NeedsRender,
		},
		{
			name: "javascript shell already rendered by the browser",
			doc:  Document{Provider: ProviderPatchright, StatusCode: 200, Type: ContentHTML, HTML: `<html><body><noscript>Please enable JavaScript to run this app.</noscript><div id="root"><nav>Home</nav></div><script src="/1.js"></script><script src="/2.js"></script><script src="/3.js"></script><script src="/4.js"></script></body></html>`},
			want: Accept,
		},
		{
			name: "block page rendered by the browser",
			doc:  Document{Provider: ProviderPatchright, StatusCode: 403, Type: ContentHTML, HTML: `<html><title>Just a moment...</title><body><form id="challenge-form" action="?__cf_chl_f_tk=abc"></form></body></html>`},
			want: Blocked,
		},
		{
			name: "cloudflare block",
			doc:  Document{StatusCode: 403, Type: ContentHTML, HTML: `<html><title>Just a moment...</title><body><form id="challenge-form" action="?__cf_chl_f_tk=abc"></form></body></html>`},
			want: Blocked,
		},
		{
			name: "missing resource",
			doc:  Document{StatusCode: 404, Type: ContentHTML, HTML: "<html><body>missing</body></html>"},
			want: Dead,
		},
		{
			name: "cloudflare challenge with custom title, detected by URL",
			doc:  Document{StatusCode: 200, Type: ContentHTML, FinalURL: "https://www.glassdoor.com/Reviews/index.htm?__cf_chl_rt_tk=abc", HTML: "<html><title>Security | Glassdoor</title><body><p>" + strings.Repeat("Checking things. ", 300) + "</p></body></html>"},
			want: Blocked,
		},
		{
			name: "cloudflare challenge header on a long page",
			doc:  Document{StatusCode: 200, Type: ContentHTML, Headers: map[string]string{"CF-Mitigated": "challenge"}, HTML: "<html><body><p>" + strings.Repeat("Useful content. ", 300) + "</p></body></html>"},
			want: Blocked,
		},
		{
			name: "challenge left after browser settling",
			doc:  Document{StatusCode: 200, Type: ContentHTML, Challenge: &Challenge{Vendor: ChallengeDataDome, URL: "https://geo.captcha-delivery.com/captcha/?t=fe"}, HTML: "<html><body><p>" + strings.Repeat("Useful content. ", 300) + "</p></body></html>"},
			want: Blocked,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := Classify(test.doc); got.Kind != test.want {
				t.Errorf("Document() kind = %q, want %q; reason = %s", got.Kind, test.want, got.Reason)
			}
		})
	}
}
