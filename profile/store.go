package profile

import (
	"errors"
	"regexp"
)

var namePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$`)

func Validate(name string) error {
	if !namePattern.MatchString(name) {
		return errors.New("profile name must contain 1–64 letters, numbers, underscores, or hyphens and start with a letter or number")
	}
	return nil
}
