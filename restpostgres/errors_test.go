package restpostgres

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestIsNotFound(t *testing.T) {
	a := &postgresAdapter{}
	if !a.IsNotFound(pgx.ErrNoRows) {
		t.Error("IsNotFound(pgx.ErrNoRows) = false, want true")
	}
	if a.IsNotFound(errors.New("boom")) {
		t.Error("IsNotFound(plain error) = true, want false")
	}
	if a.IsNotFound(nil) {
		t.Error("IsNotFound(nil) = true, want false")
	}
}

func TestIsDuplicateKey(t *testing.T) {
	a := &postgresAdapter{}
	if !a.IsDuplicateKey(&pgconn.PgError{Code: "23505"}) {
		t.Error("IsDuplicateKey(23505) = false, want true")
	}
	if a.IsDuplicateKey(&pgconn.PgError{Code: "42703"}) {
		t.Error("IsDuplicateKey(other code) = true, want false")
	}
}

func TestIsClientErrorAlwaysFalse(t *testing.T) {
	a := &postgresAdapter{}
	if a.IsClientError(errors.New("anything")) {
		t.Error("IsClientError must always be false: client input is validated in Go before any SQL")
	}
	if a.IsClientError(nil) {
		t.Error("IsClientError(nil) = true, want false")
	}
}

func TestIsTimeout(t *testing.T) {
	a := &postgresAdapter{}
	if !a.IsTimeout(context.DeadlineExceeded) {
		t.Error("IsTimeout(context.DeadlineExceeded) = false, want true")
	}
	if !a.IsTimeout(&pgconn.PgError{Code: "57014"}) {
		t.Error("IsTimeout(57014) = false, want true")
	}
	if a.IsTimeout(errors.New("boom")) {
		t.Error("IsTimeout(plain error) = true, want false")
	}
}

// TestBigintOverflowIsNeverClassifiedAsClientOrDuplicate locks in
// postgres-rfc.md §7: a version overflow (22003) is an internal
// failure, never IsDuplicateKey/IsClientError/IsTimeout.
func TestBigintOverflowIsNeverMisclassified(t *testing.T) {
	a := &postgresAdapter{}
	err := &pgconn.PgError{Code: "22003"}
	if a.IsDuplicateKey(err) || a.IsClientError(err) || a.IsTimeout(err) {
		t.Error("22003 (numeric_value_out_of_range) must not be classified as duplicate-key, client, or timeout")
	}
}
