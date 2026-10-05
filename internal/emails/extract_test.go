package emails

import (
	"encoding/hex"
	"strings"
	"testing"

	"github.com/simonbalfe/pagelode/internal/page"
)

func TestExtractAddresses(t *testing.T) {
	encoded := []byte{42}
	for _, b := range []byte("cloud@example.org") {
		encoded = append(encoded, b^42)
	}
	document := page.Document{FinalURL: "https://example.org/contact", HTML: `<html><body><footer>FOOTER@example.org.</footer><a href="mailto:First%40example.org,second@example.org?subject=ignore@example.org">Email us</a><p>person [at] example [dot] org</p><p><span>split</span><span>@example.org</span></p><span data-cfemail="` + hex.EncodeToString(encoded) + `">hidden</span><script type="application/ld+json">{"email":"schema@example.org","token":"secret@example.org"}</script><script>const sample="script@example.org";</script><div hidden>hidden@example.org</div></body></html>`, Traffic: &page.Capture{Entries: []page.Exchange{{URL: "https://example.org/api/people?token=private", Status: 200, ResponseBody: `{"people":[{"email":"network@example.org"}],"token":"secret@example.org"}`}, {Status: 403, ResponseBody: `{"email":"blocked@example.org"}`}}}}
	found := extractAddresses(document)
	addresses := map[string]bool{}
	for _, item := range found {
		addresses[item.address] = true
	}
	expected := []string{"footer@example.org", "first@example.org", "second@example.org", "person@example.org", "split@example.org", "cloud@example.org", "schema@example.org", "network@example.org"}
	if len(addresses) != len(expected) {
		t.Fatalf("addresses: %#v", addresses)
	}
	for _, address := range expected {
		if !addresses[address] {
			t.Errorf("missing %s", address)
		}
	}
	for _, item := range found {
		if item.address == "network@example.org" && (item.kind != "network_json" || !strings.Contains(item.source, "/api/people")) {
			t.Errorf("network source: %#v", item)
		}
	}
}

func TestJSONAndTextAddresses(t *testing.T) {
	for _, test := range []struct {
		text string
		want string
	}{
		{`{"contacts":{"emailAddresses":["a@example.com","A@example.com"]},"password":"secret@example.com"}`, "a@example.com"},
		{"Contact: simple@example.com.", "simple@example.com"},
	} {
		found := extractAddresses(page.Document{Text: test.text})
		if len(found) != 1 || found[0].address != test.want {
			t.Errorf("%q: %#v", test.text, found)
		}
	}
}
