package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestReadTargetsNormalizesDeduplicatesAndSkipsInvalid(t *testing.T) {
	path := filepath.Join(t.TempDir(), "targets.txt")
	content := "example.com\nhttps://example.com\n\nhttps://user:pass@secret.example/\nftp://files.example\nhttp://other.example/page\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := readTargets(path)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"https://example.com/", "http://other.example/page"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("readTargets() = %v, want %v", got, want)
	}
}
