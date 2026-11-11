package postgres

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

// Pure-function tests: no database needed, so these run even without
// POSTGRES_TEST_URI (e.g. under `make unit`).

func TestWhereClauseEmpty(t *testing.T) {
	where, args := whereClause(nil, 0)
	if where != "TRUE" || len(args) != 0 {
		t.Fatalf("whereClause(nil, 0) = %q, %v", where, args)
	}
	where, args = whereClause(Filter{}, 0)
	if where != "TRUE" || len(args) != 0 {
		t.Fatalf("whereClause({}, 0) = %q, %v", where, args)
	}
}

func TestWhereClauseEqualityOnly(t *testing.T) {
	// Column order must be deterministic (sorted), not map iteration order.
	where, args := whereClause(Filter{"b": 2, "a": 1}, 0)
	if want := `"a" = $1 AND "b" = $2`; where != want {
		t.Fatalf("where = %q, want %q", where, want)
	}
	if len(args) != 2 || args[0] != 1 || args[1] != 2 {
		t.Fatalf("args = %v", args)
	}
}

func TestWhereClauseNil(t *testing.T) {
	// A nil value must render IS NULL, not "= $N" (= NULL never matches
	// in SQL) — and must not consume a placeholder number.
	where, args := whereClause(Filter{"a": nil, "b": 2}, 0)
	if want := `"a" IS NULL AND "b" = $1`; where != want {
		t.Fatalf("where = %q, want %q", where, want)
	}
	if len(args) != 1 || args[0] != 2 {
		t.Fatalf("args = %v", args)
	}
}

func TestWhereClauseAllNil(t *testing.T) {
	where, args := whereClause(Filter{"a": nil}, 0)
	if where != `"a" IS NULL` {
		t.Fatalf("where = %q", where)
	}
	if len(args) != 0 {
		t.Fatalf("args = %v, want none", args)
	}
}

func TestWhereClauseStartAt(t *testing.T) {
	// startAt offsets placeholder numbers, e.g. when the caller already
	// used $1..$N for something else (UpdateOne's SET list).
	where, args := whereClause(Filter{"a": 1}, 3)
	if want := `"a" = $4`; where != want {
		t.Fatalf("where = %q, want %q", where, want)
	}
	if len(args) != 1 || args[0] != 1 {
		t.Fatalf("args = %v", args)
	}
}

func TestColumnListStar(t *testing.T) {
	if got := columnList(nil); got != "*" {
		t.Fatalf("columnList(nil) = %q", got)
	}
	if got := columnList([]string{}); got != "*" {
		t.Fatalf("columnList([]) = %q", got)
	}
}

func TestColumnListExplicit(t *testing.T) {
	if got := columnList([]string{"b", "a"}); got != `"b", "a"` {
		t.Fatalf("columnList = %q", got)
	}
}

func TestIsDuplicateKeyErrorFalse(t *testing.T) {
	if IsDuplicateKeyError(nil) {
		t.Error("nil should not be a duplicate key error")
	}
	if IsDuplicateKeyError(errors.New("boom")) {
		t.Error("a plain error should not be a duplicate key error")
	}
	if IsDuplicateKeyError(&pgconn.PgError{Code: "42703"}) {
		t.Error("a PgError with another code should not be a duplicate key error")
	}
}

func TestIsDuplicateKeyErrorTrue(t *testing.T) {
	if !IsDuplicateKeyError(&pgconn.PgError{Code: uniqueViolation}) {
		t.Error("a PgError with code 23505 should be a duplicate key error")
	}
}

func TestIsTimeout(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"plain error", errors.New("boom"), false},
		{"deadline exceeded", context.DeadlineExceeded, true},
		{"wrapped deadline exceeded", errFmt(context.DeadlineExceeded), true},
		{"query canceled", &pgconn.PgError{Code: queryCanceled}, true},
		{"other pg error", &pgconn.PgError{Code: "42703"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := IsTimeout(c.err); got != c.want {
				t.Errorf("IsTimeout(%v) = %v, want %v", c.err, got, c.want)
			}
		})
	}
}

// errFmt wraps err the way context/network code typically does, to check
// IsTimeout unwraps rather than only matching the exact error value.
func errFmt(err error) error { return fmt.Errorf("wrapped: %w", err) }
