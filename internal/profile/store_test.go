package profile

import (
	"os"
	"path/filepath"
	"testing"
)

func TestProfileDirectories(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"../other", "a/b", ".", "", "a b"} {
		if _, err := Directory(root, name, true); err == nil {
			t.Errorf("accepted name %q", name)
		}
	}
	if _, err := Directory(root, "missing", false); err == nil {
		t.Error("accepted missing profile")
	}
	directory, err := Directory(root, "account", true)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(directory)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0700 {
		t.Errorf("mode=%o,want 700", info.Mode().Perm())
	}
	if err := os.Symlink(directory, filepath.Join(root, "alias")); err != nil {
		t.Fatal(err)
	}
	if _, err := Directory(root, "alias", false); err == nil {
		t.Error("accepted symlink profile")
	}
}
