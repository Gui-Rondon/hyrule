package postgres

import (
	"context"
	"strings"
	"testing"
)

func TestEnsureFunctionsIdempotent(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	if err := EnsureFunctions(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if err := EnsureFunctions(ctx, pool); err != nil {
		t.Fatalf("second call (CREATE OR REPLACE) must not fail: %v", err)
	}
}

func TestHyruleSafeInteger(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	if err := EnsureFunctions(ctx, pool); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		in   any // nil or string
		want any // nil or string (numeric rendered as text for comparison)
	}{
		{"123", "123"},
		{"-45", "-45"},
		{"1.5", nil}, // not an integer textually
		{"abc", nil}, // not numeric at all
		{"", nil},    // empty
		{nil, nil},   // SQL NULL in, SQL NULL out
	}
	for _, c := range cases {
		var got *string
		err := pool.QueryRow(ctx, "SELECT hyrule_safe_integer($1)::text", c.in).Scan(&got)
		if err != nil {
			t.Fatalf("hyrule_safe_integer(%v): %v", c.in, err)
		}
		switch want := c.want.(type) {
		case nil:
			if got != nil {
				t.Errorf("hyrule_safe_integer(%v) = %v, want NULL", c.in, *got)
			}
		case string:
			if got == nil || *got != want {
				t.Errorf("hyrule_safe_integer(%v) = %v, want %v", c.in, got, want)
			}
		}
	}
}

func TestHyruleSafeIntegerNeverRaises(t *testing.T) {
	// The function's job is to turn a malformed/overflowing value into
	// SQL NULL, never into a raised error that would abort the whole
	// query -- confirmed with a value far beyond any native integer
	// type, which must still return cleanly.
	pool := testPool(t)
	ctx := context.Background()
	if err := EnsureFunctions(ctx, pool); err != nil {
		t.Fatal(err)
	}
	huge := strings.Repeat("9", 50)
	var got *string
	if err := pool.QueryRow(ctx, "SELECT hyrule_safe_integer($1)::text", huge).Scan(&got); err != nil {
		t.Fatalf("unexpected error (function must never raise): %v", err)
	}
	if got == nil || *got != huge {
		t.Errorf("hyrule_safe_integer(50 nines) = %v, want the exact value (numeric has no such magnitude limit)", got)
	}
}

func TestHyruleSafeNumeric(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	if err := EnsureFunctions(ctx, pool); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		in   string
		want *string
	}{
		{"1.5", new("1.5")},
		{"-1.5", new("-1.5")},
		{"123", new("123")},
		{"1e3", nil}, // scientific notation rejected
		{"abc", nil},
	}
	for _, c := range cases {
		var got *string
		if err := pool.QueryRow(ctx, "SELECT hyrule_safe_numeric($1)::text", c.in).Scan(&got); err != nil {
			t.Fatalf("hyrule_safe_numeric(%q): %v", c.in, err)
		}
		if (got == nil) != (c.want == nil) || (got != nil && *got != *c.want) {
			t.Errorf("hyrule_safe_numeric(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestHyruleSafeTimestamptz(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	if err := EnsureFunctions(ctx, pool); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		in    string
		valid bool
	}{
		{"2024-01-02T15:04:05Z", true},
		{"2024-01-02T15:04:05.123Z", true},
		{"2024-01-02T15:04:05+02:00", true},
		{"2024-01-02 15:04:05", false}, // no offset, no 'T'
		{"not-a-date", false},
		{"", false},
	}
	for _, c := range cases {
		var got *string
		if err := pool.QueryRow(ctx, "SELECT hyrule_safe_timestamptz($1)::text", c.in).Scan(&got); err != nil {
			t.Fatalf("hyrule_safe_timestamptz(%q): %v", c.in, err)
		}
		if (got != nil) != c.valid {
			t.Errorf("hyrule_safe_timestamptz(%q) = %v, want valid=%v", c.in, got, c.valid)
		}
	}
}
