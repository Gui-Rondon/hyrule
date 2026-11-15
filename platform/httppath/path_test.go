package httppath

import "testing"

func TestNormalizePrefix(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{"", ""},
		{"/", ""},
		{"///", ""},
		{"auth", "/auth"},
		{"/auth/", "/auth"},
		{"//auth/api///", "/auth/api"},
	} {
		if got := NormalizePrefix(tc.input); got != tc.want {
			t.Errorf("NormalizePrefix(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestStripPrefix(t *testing.T) {
	for _, tc := range []struct {
		path, prefix, want string
		ok                 bool
	}{
		{"/auth", "/auth", "/", true},
		{"/auth/", "/auth", "/", true},
		{"/auth/items", "/auth", "/items", true},
		{"/auth/items/1", "/auth", "/items/1", true},
		{"/authz", "/auth", "", false},
		{"/other", "/auth", "", false},
		{"/items", "", "/items", true},
	} {
		got, ok := StripPrefix(tc.path, tc.prefix)
		if got != tc.want || ok != tc.ok {
			t.Errorf("StripPrefix(%q, %q) = (%q, %t), want (%q, %t)", tc.path, tc.prefix, got, ok, tc.want, tc.ok)
		}
	}
}

func TestTrimOuterSlashes(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{"", ""},
		{"/", ""},
		{"/items/1/", "items/1"},
		{"items", "items"},
	} {
		if got := TrimOuterSlashes(tc.input); got != tc.want {
			t.Errorf("TrimOuterSlashes(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}
