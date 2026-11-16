package restpostgres

import (
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/Gui-Rondon/hyrule/platform/postgres"
)

// IsNotFound reports whether err means a lookup found no document.
func (a *postgresAdapter) IsNotFound(err error) bool {
	return errors.Is(err, pgx.ErrNoRows)
}

// IsDuplicateKey reports whether err means a unique index was violated.
func (a *postgresAdapter) IsDuplicateKey(err error) bool {
	return postgres.IsDuplicateKeyError(err)
}

// IsClientError reports whether err was caused by the client's query
// itself. It always returns false: every piece of client input (filter
// values, PATCH bodies) is validated in Go, against SchemaSpec and
// rest.Schema, before any SQL is sent -- so a client can never cause a
// SQL-level error on its own.
func (a *postgresAdapter) IsClientError(err error) bool { return false }

// IsTimeout reports whether err means the operation ran out of time,
// or Postgres aborted the transaction because of a conflict with a
// concurrent one (deadlock_detected/serialization_failure) -- section
// 12 of the Postgres portability plan maps both to 503, the same as a
// plain statement timeout.
func (a *postgresAdapter) IsTimeout(err error) bool {
	return postgres.IsTimeout(err) || postgres.IsTransientConflict(err)
}

// ClassifiesWriteTimeouts is always true: platform/postgres's
// Transactor has no label-based transaction retry to lose (a plain
// BeginTx/fn/Commit/Rollback) -- nothing is lost by classifying
// 57014/40P01/40001 as 503 from inside the write's own transaction
// closure, same as queryError already does unconditionally for reads.
func (a *postgresAdapter) ClassifiesWriteTimeouts() bool { return true }
