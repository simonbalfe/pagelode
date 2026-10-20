package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestEmailsRejectsFlagsFromTheOtherMode(t *testing.T) {
	for _, test := range []struct {
		args []string
		want string
	}{
		{[]string{"--database", "postgres://localhost/pagelode", "example.com"}, "--database and --workers apply only with --input"},
		{[]string{"--workers", "8", "example.com"}, "--database and --workers apply only with --input"},
		{[]string{"--input", "sites.txt", "--profile", "account"}, "--profile and --verbose do not apply with --input"},
		{[]string{"--input", "sites.txt", "--verbose"}, "--profile and --verbose do not apply with --input"},
		{[]string{"--input", "sites.txt", "example.com"}, "do not also pass a URL"},
		{[]string{"--input", "sites.txt"}, "PAGELODE_DATABASE_URL"},
	} {
		var output bytes.Buffer
		err := emailsCommand(Config{}, test.args, &output, &output)
		if err == nil || !strings.Contains(err.Error(), test.want) {
			t.Errorf("emails %v: error = %v, want %q", test.args, err, test.want)
		}
	}
}

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
