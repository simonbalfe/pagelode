package proxy

import "testing"

func TestWithSession(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		raw    string
		want   string
		sticky bool
	}{
		{name: "placeholder in username", raw: "http://user-{session}:secret@proxy.example:1000", want: "http://user-abc123:secret@proxy.example:1000", sticky: true},
		{name: "encoded placeholder", raw: "http://user-%7Bsession%7D:secret@proxy.example:1000", want: "http://user-abc123:secret@proxy.example:1000", sticky: true},
		{name: "provider session in password", raw: "http://user:key_country-gb_session-old999@proxy.example:1000", want: "http://user:key_country-gb_session-abc123@proxy.example:1000", sticky: true},
		{name: "hard session in password", raw: "http://user:key_hardsession-old999@proxy.example:1000", want: "http://user:key_hardsession-abc123@proxy.example:1000", sticky: true},
		{name: "no session", raw: "http://user:secret@proxy.example:1000", want: "http://user:secret@proxy.example:1000", sticky: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := WithSession(test.raw, "abc123"); got != test.want {
				t.Errorf("WithSession(%q) = %q, want %q", test.raw, got, test.want)
			}
			if got := Sticky(test.raw); got != test.sticky {
				t.Errorf("Sticky(%q) = %v, want %v", test.raw, got, test.sticky)
			}
		})
	}
}
