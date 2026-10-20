package fetch

import "testing"

func TestValidate(t *testing.T) {
	for _, name := range []string{"../other", "a/b", ".", "", "a b", "-lead"} {
		if err := ValidateProfile(name); err == nil {
			t.Errorf("Validate(%q) = nil, want error", name)
		}
	}
	for _, name := range []string{"account", "a", "Team_1-x"} {
		if err := ValidateProfile(name); err != nil {
			t.Errorf("Validate(%q) = %v, want nil", name, err)
		}
	}
}
