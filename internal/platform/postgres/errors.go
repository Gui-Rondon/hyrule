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

// IsDuplicateKeyError reports a unique constraint violation, mirroring
// mongo.IsDuplicateKeyError for the error translation postgres-rfc.md's
// point 3 still needs a home for.
func IsDuplicateKeyError(err error) bool {
	pgErr, ok := errors.AsType[*pgconn.PgError](err)
	return ok && pgErr.Code == uniqueViolation
}

// IsTimeout mirrors mongo.IsTimeout: a statement or context timeout.
func IsTimeout(err error) bool {
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok {
		return pgErr.Code == queryCanceled
	}
	return errors.Is(err, context.DeadlineExceeded)
}
