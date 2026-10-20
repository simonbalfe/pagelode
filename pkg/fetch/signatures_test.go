package fetch

import "testing"

func TestEmbeddedSignaturesCompile(t *testing.T) {
	t.Parallel()
	set, err := parse(raw)
	if err != nil {
		t.Fatalf("parse(signatures.json) error = %v", err)
	}
	if len(set.Blocks) == 0 || len(set.ShortBlocks) == 0 || len(set.ChallengeHeaders) == 0 || len(set.ChallengeURLs) == 0 {
		t.Fatalf("parse(signatures.json) = %+v; want every Go section populated", set)
	}
}

func TestParseRejectsUnsupportedFlags(t *testing.T) {
	t.Parallel()
	_, err := parse([]byte(`{"blocks":[{"label":"x","pattern":"a","flags":"g"}]}`))
	if err == nil {
		t.Fatal("parse(flags g) error = nil; want unsupported flags error")
	}
}
