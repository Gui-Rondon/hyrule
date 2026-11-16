// Package sqlite provides the experimental, local SQLite backend. It needs no
// external process or C compiler. Transactions belong to one database handle.
package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	_ "modernc.org/sqlite"
)

// Open opens a file (or :memory:) and verifies the connection. The caller closes
// it. A single connection serializes local writers and keeps :memory: coherent.
func Open(ctx context.Context, path string) (*sql.DB, error) {
	if path == "" {
		return nil, errors.New("sqlite: path must not be empty")
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err = db.ExecContext(ctx, "PRAGMA busy_timeout=5000"); err != nil {
		db.Close()
		return nil, fmt.Errorf("sqlite: open: %w", err)
	}
	return db, nil
}

// Querier is implemented by a database and a transaction.
type Querier interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

type transactionKey struct{}
type transaction struct {
	db *sql.DB
	tx *sql.Tx
}

// Transactor joins an ambient same-database transaction or starts a new one.
type Transactor struct{ DB *sql.DB }

// Querier returns the transaction in ctx, when it belongs to this database.
func (t Transactor) Querier(ctx context.Context) Querier {
	if current, ok := ctx.Value(transactionKey{}).(transaction); ok && current.db == t.DB {
		return current.tx
	}
	return t.DB
}

// WithTransaction commits only if fn succeeds. Panics also roll back.
func (t Transactor) WithTransaction(ctx context.Context, fn func(context.Context) error) error {
	if t.DB == nil {
		return errors.New("sqlite: nil database")
	}
	if current, ok := ctx.Value(transactionKey{}).(transaction); ok {
		if current.db != t.DB {
			return errors.New("sqlite: cannot join a transaction from another database")
		}
		return fn(ctx)
	}
	tx, err := t.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := fn(context.WithValue(ctx, transactionKey{}, transaction{t.DB, tx})); err != nil {
		return err
	}
	return tx.Commit()
}
