package postgres

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// defaultRollbackTimeout bounds the cleanup Rollback issued when fn
// returns an error or panics, so a slow/wedged connection can't hang
// WithTransaction forever on its way out.
const defaultRollbackTimeout = 5 * time.Second

type txEntry struct {
	pool *pgxpool.Pool
	tx   pgx.Tx
}

type txKey struct{}

var (
	// ErrNestedTransaction reports that ctx already carries a
	// transaction on the same pool -- WithTransaction never nests.
	ErrNestedTransaction = errors.New("postgres: context already carries a transaction on this pool")
	// ErrForeignTransaction reports that ctx carries a transaction, but
	// from a different pool than this Transactor's.
	ErrForeignTransaction = errors.New("postgres: context carries a transaction from a different pool")
)

// Transactor runs fn inside a single Postgres transaction, exposed to fn
// (and to anything fn calls, such as Store's methods) via ctx. It never
// nests: a ctx already carrying a transaction on Pool is rejected with
// ErrNestedTransaction, and one from another pool with
// ErrForeignTransaction -- restpostgres (Fase 2) is responsible for not
// composing two independent resources' transactions.
type Transactor struct {
	Pool *pgxpool.Pool
	// RollbackTimeout bounds the cleanup Rollback; zero means
	// defaultRollbackTimeout.
	RollbackTimeout time.Duration
}

func (t Transactor) rollbackTimeout() time.Duration {
	if t.RollbackTimeout > 0 {
		return t.RollbackTimeout
	}
	return defaultRollbackTimeout
}

// WithTransaction begins a transaction, runs fn with a context carrying
// it, and commits on success. On any error from fn (or from Commit), the
// transaction is rolled back with a timeout independent of ctx's own
// cancellation, so a caller that already canceled ctx still gets a clean
// rollback instead of an abandoned connection.
func (t Transactor) WithTransaction(ctx context.Context, fn func(context.Context) error) error {
	if e, ok := ctx.Value(txKey{}).(txEntry); ok {
		if e.pool == t.Pool {
			return ErrNestedTransaction
		}
		return ErrForeignTransaction
	}
	tx, err := t.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() {
		rbCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), t.rollbackTimeout())
		defer cancel()
		if err := tx.Rollback(rbCtx); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
			slog.Error("postgres: rollback failed", "error", err)
		}
	}()
	if err := fn(context.WithValue(ctx, txKey{}, txEntry{pool: t.Pool, tx: tx})); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
