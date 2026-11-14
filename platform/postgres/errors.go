package postgres

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
)

// uniqueViolation is Postgres' error code for a unique constraint
// violation — the equivalent of Mongo's duplicate key error.
const uniqueViolation = "23505"

// queryCanceled is raised on statement_timeout.
const queryCanceled = "57014"

// deadlockDetected and serializationFailure are raised when Postgres
// aborts a transaction because of a conflict with a concurrent one,
// not because of anything wrong with the statement itself -- see
// IsTransientConflict.
const (
	deadlockDetected     = "40P01"
	serializationFailure = "40001"
)

// pgCode extracts err's SQLSTATE, the one bit of a *pgconn.PgError
// every classifier below compares against -- shared so a new category
// never has to re-walk the error chain with its own errors.AsType call.
func pgCode(err error) (string, bool) {
	pgErr, ok := errors.AsType[*pgconn.PgError](err)
	if !ok {
		return "", false
	}
	return pgErr.Code, true
}

// IsDuplicateKeyError reports a unique constraint violation, mirroring
// mongo.IsDuplicateKeyError for the error translation postgres-rfc.md's
// point 3 still needs a home for.
func IsDuplicateKeyError(err error) bool {
	code, ok := pgCode(err)
	return ok && code == uniqueViolation
}

// IsTimeout mirrors mongo.IsTimeout: a statement or context timeout.
func IsTimeout(err error) bool {
	if code, ok := pgCode(err); ok {
		return code == queryCanceled
	}
	return errors.Is(err, context.DeadlineExceeded)
}

// IsTransientConflict reports a deadlock (40P01) or a serialization
// failure (40001) -- section 12 of the Postgres portability plan
// classifies both the same as a timeout for the client (safe to
// retry, never a 500): restpostgres's postgresAdapter.IsTimeout folds
// this in, rather than this function being folded into IsTimeout
// itself, because neither is actually a timeout -- the pgconn.PgError
// logged alongside already carries the real SQLSTATE, so the two stay
// their own category for observability without a second predicate on
// rest.Adapter.
func IsTransientConflict(err error) bool {
	code, ok := pgCode(err)
	return ok && (code == deadlockDetected || code == serializationFailure)
}
