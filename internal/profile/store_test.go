package profile

import "testing"

func TestValidate(t *testing.T) {
	for _, name := range []string{"../other", "a/b", ".", "", "a b", "-lead"} {
		if err := Validate(name); err == nil {
			t.Errorf("Validate(%q) = nil, want error", name)
		}
	}
	for _, name := range []string{"account", "a", "Team_1-x"} {
		if err := Validate(name); err != nil {
			t.Errorf("Validate(%q) = %v, want nil", name, err)
		}
	}
}
