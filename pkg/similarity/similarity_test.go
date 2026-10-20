package similarity

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
)

const (
	articleA = "The quick brown fox jumps over the lazy dog while the farmer watches the river flow slowly past the old stone bridge and the village market opens for another busy morning of trade and conversation"
	articleB = "The quick brown fox jumps over the lazy dog while the farmer watches the river flow slowly past the old stone bridge and the village market opens for another busy morning of trade and discussion"
	articleC = "Quarterly revenue grew nine percent driven by subscription renewals, while operating margin narrowed as hosting costs increased; management raised full year guidance and announced a new share repurchase programme"
)

func TestText(t *testing.T) {
	tests := []struct {
		name string
		html string
		want string
	}{
		{"empty", "", ""},
		{"plain", "<p>Hello   <b>world</b></p>", "Hello world"},
		{
			name: "strips non visible elements",
			html: `<html><head><title>T</title><style>p{color:red}</style><script>var a = 1;</script></head>
<body><noscript>enable js</noscript><svg><text>icon</text></svg><template><p>tpl</p></template><p>Visible</p><p>text</p></body></html>`,
			want: "T Visible text",
		},
		{"whitespace collapsed", "<div>a\n\n\t b</div>\n<div> c </div>", "a b c"},
		{"entities decoded", "<p>fish &amp; chips</p>", "fish & chips"},
		{"malformed tolerated", "<div><p>unclosed <b>bold", "unclosed bold"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Text(tt.html); got != tt.want {
				t.Fatalf("Text() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFingerprint(t *testing.T) {
	if got := Fingerprint(""); got != 0 {
		t.Fatalf("Fingerprint(empty) = %d, want 0", got)
	}
	if got := Fingerprint(" ,.;  "); got != 0 {
		t.Fatalf("Fingerprint(punctuation) = %d, want 0", got)
	}
	if Fingerprint(articleA) != Fingerprint(articleA) {
		t.Fatal("fingerprint not deterministic")
	}
	if Fingerprint("Hello World Again") != Fingerprint("hello, world... again!") {
		t.Fatal("fingerprint should ignore case and punctuation")
	}
	if Fingerprint("alpha") == Fingerprint("beta") {
		t.Fatal("single words should produce different fingerprints")
	}
	if Fingerprint("alpha beta") == 0 {
		t.Fatal("two words should still fingerprint")
	}
}

func TestDistance(t *testing.T) {
	tests := []struct {
		name string
		a, b uint64
		want int
	}{
		{"equal", 42, 42, 0},
		{"one bit", 0, 1, 1},
		{"all bits", 0, ^uint64(0), 64},
		{"symmetric", 0b1010, 0b0101, 4},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Distance(tt.a, tt.b); got != tt.want {
				t.Fatalf("Distance() = %d, want %d", got, tt.want)
			}
			if got := Distance(tt.b, tt.a); got != tt.want {
				t.Fatalf("Distance() reversed = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestFingerprintSimilarity(t *testing.T) {
	near := Distance(Fingerprint(articleA), Fingerprint(articleB))
	far := Distance(Fingerprint(articleA), Fingerprint(articleC))
	t.Logf("near=%d far=%d", near, far)
	if near > 12 {
		t.Fatalf("near-identical pages distance = %d, want <= 12", near)
	}
	if far < 20 {
		t.Fatalf("different pages distance = %d, want >= 20", far)
	}
	if near >= far {
		t.Fatalf("near distance %d should be below far distance %d", near, far)
	}
}

func TestFingerprintFromHTML(t *testing.T) {
	a := `<html><head><script>track(1)</script></head><body><main>` + articleA + `</main></body></html>`
	b := `<html><head><script>track(2); more()</script><style>x{}</style></head><body><main>` + articleA + `</main></body></html>`
	if d := Distance(Fingerprint(Text(a)), Fingerprint(Text(b))); d != 0 {
		t.Fatalf("pages differing only in scripts/styles distance = %d, want 0", d)
	}
}

func TestIndexSeen(t *testing.T) {
	a, b, c := Fingerprint(articleA), Fingerprint(articleB), Fingerprint(articleC)
	tests := []struct {
		name string
		max  int
		seq  []uint64
		want []bool
	}{
		{"exact duplicates", 0, []uint64{a, a, c}, []bool{false, true, false}},
		{"near duplicate within distance", 64, []uint64{a, c}, []bool{false, true}},
		{"near duplicate outside distance", 0, []uint64{a, b}, []bool{false, false}},
		{"near duplicate matched", Distance(a, b), []uint64{a, b, c}, []bool{false, true, false}},
		{"negative distance never matches", -1, []uint64{a, a}, []bool{false, false}},
		{"empty index", 3, nil, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			idx := NewIndex(tt.max)
			for i, fp := range tt.seq {
				if got := idx.Seen(fp); got != tt.want[i] {
					t.Fatalf("Seen #%d = %v, want %v", i, got, tt.want[i])
				}
			}
		})
	}
}

func TestIndexConcurrent(t *testing.T) {
	idx := NewIndex(0)
	var fresh atomic.Int64
	var wg sync.WaitGroup
	for i := range 40 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if !idx.Seen(Fingerprint(fmt.Sprintf("page number %d", i%10))) {
				fresh.Add(1)
			}
		}(i)
	}
	wg.Wait()
	if fresh.Load() != 10 {
		t.Fatalf("fresh = %d, want 10", fresh.Load())
	}
}
