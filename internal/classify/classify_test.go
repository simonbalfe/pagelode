package classify

import (
	"strings"
	"testing"

	"github.com/simonbalfe/pagelode/internal/page"
)

func TestDocument(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		doc  page.Document
		want Kind
	}{
		{
			name: "usable page",
			doc:  page.Document{StatusCode: 200, Type: page.ContentHTML, HTML: "<html><body><main><h1>Example</h1><p>" + strings.Repeat("Useful content. ", 20) + "</p></main></body></html>"},
			want: Accept,
		},
		{
			name: "javascript shell",
			doc:  page.Document{StatusCode: 200, Type: page.ContentHTML, HTML: `<html><body><div id="root"></div><script src="/1.js"></script><script src="/2.js"></script><script src="/3.js"></script><script src="/4.js"></script></body></html>`},
			want: NeedsRender,
		},
		{
			name: "cloudflare block",
			doc:  page.Document{StatusCode: 403, Type: page.ContentHTML, HTML: `<html><title>Just a moment...</title><body><form id="challenge-form" action="?__cf_chl_f_tk=abc"></form></body></html>`},
			want: Blocked,
		},
		{
			name: "missing resource",
			doc:  page.Document{StatusCode: 404, Type: page.ContentHTML, HTML: "<html><body>missing</body></html>"},
			want: Dead,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := Document(test.doc); got.Kind != test.want {
				t.Errorf("Document() kind = %q, want %q; reason = %s", got.Kind, test.want, got.Reason)
			}
		})
	}
}
