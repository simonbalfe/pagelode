package profile

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

var namePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$`)

func Validate(name string) error {
	if !namePattern.MatchString(name) {
		return errors.New("profile name must contain 1–64 letters, numbers, underscores, or hyphens and start with a letter or number")
	}
	return nil
}

func Directory(root, name string, create bool) (string, error) {
	if err := Validate(name); err != nil {
		return "", err
	}
	if root == "" {
		return "", errors.New("profile directory is not configured")
	}
	directory, err := filepath.Abs(filepath.Join(root, name))
	if err != nil {
		return "", fmt.Errorf("resolve profile: %w", err)
	}
	if create {
		if err := os.MkdirAll(directory, 0700); err != nil {
			return "", fmt.Errorf("create profile: %w", err)
		}
	}
	info, err := os.Lstat(directory)
	if errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("profile %q does not exist; run pagelode profile login %s <URL>", name, name)
	}
	if err != nil {
		return "", fmt.Errorf("read profile: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("profile must be a directory, not a symlink")
	}
	if err := os.Chmod(directory, 0700); err != nil {
		return "", fmt.Errorf("protect profile: %w", err)
	}
	return directory, nil
}
