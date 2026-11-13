// Package postgres is a scaffold: a Store-shaped type that proves the
// pattern platform/mongodb.Store already follows (its own types,
// not an implementation of rest.Store) works for a second backend. See
// the "Adding a Postgres backend" design notes
// (website/docs/reference/postgres-rfc.md) for what's still open —
// most notably, no resource is wired to this yet.
package postgres

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// querier is the subset of *pgxpool.Pool and pgx.Tx that Store's methods
// need -- defining it lets Store run against either, so a method called
// with a ctx that carries a Transactor's transaction (see
// transaction.go) joins that transaction instead of opening a new,
// unrelated connection.
type querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	SendBatch(ctx context.Context, b *pgx.Batch) pgx.BatchResults
}

// querier returns the ambient transaction carried by ctx if it was
// opened on s.Pool (via Transactor.WithTransaction), falling back to
// s.Pool itself -- the only path before this method existed, so a ctx
// with no transaction behaves exactly as it always did.
func (s Store) querier(ctx context.Context) querier {
	if e, ok := ctx.Value(txKey{}).(txEntry); ok && e.pool == s.Pool {
		return e.tx
	}
	return s.Pool
}

// Store wraps a *pgxpool.Pool and one table. Unlike mongodb.Store, this
// does not implement rest.Store — see postgres-rfc.md's point 1.
type Store struct {
	Pool  *pgxpool.Pool
	Table string
}

// NewStore wraps pool for table.
func NewStore(pool *pgxpool.Pool, table string) Store { return Store{Pool: pool, Table: table} }

// Filter is equality only — column: value, ANDed together. No Mongo-style
// operators; a richer filter language is explicitly not decided yet
// (postgres-rfc.md's point 1).
type Filter map[string]any

// SortField is one ORDER BY term.
type SortField struct {
	Column string
	Desc   bool
}

func (s Store) table() string { return pgx.Identifier{s.Table}.Sanitize() }

// whereClause renders filter as a parameterized WHERE clause (no WHERE
// keyword), sorted by column for a deterministic query across calls.
// An empty filter renders "TRUE" (matches every row). A nil value renders
// "col IS NULL" instead of "col = $N": in SQL, "= NULL" never evaluates
// true (it's UNKNOWN), so treating it as an equality would silently
// match nothing instead of what a Mongo-shaped caller means by
// Filter{"col": nil}.
func whereClause(filter Filter, startAt int) (string, []any) {
	if len(filter) == 0 {
		return "TRUE", nil
	}
	cols := make([]string, 0, len(filter))
	for col := range filter {
		cols = append(cols, col)
	}
	sort.Strings(cols)
	parts := make([]string, 0, len(cols))
	args := make([]any, 0, len(cols))
	for _, col := range cols {
		ident := pgx.Identifier{col}.Sanitize()
		if filter[col] == nil {
			parts = append(parts, ident+" IS NULL")
			continue
		}
		args = append(args, filter[col])
		parts = append(parts, fmt.Sprintf("%s = $%d", ident, startAt+len(args)))
	}
	return strings.Join(parts, " AND "), args
}

func columnList(columns []string) string {
	if len(columns) == 0 {
		return "*"
	}
	quoted := make([]string, len(columns))
	for i, c := range columns {
		quoted[i] = pgx.Identifier{c}.Sanitize()
	}
	return strings.Join(quoted, ", ")
}

// Find lists rows; limit <= 0 means no LIMIT/OFFSET.
func (s Store) Find(ctx context.Context, filter Filter, skip, limit int64, order []SortField, columns []string) ([]map[string]any, error) {
	where, args := whereClause(filter, 0)
	q := fmt.Sprintf("SELECT %s FROM %s WHERE %s", columnList(columns), s.table(), where)
	if len(order) > 0 {
		terms := make([]string, len(order))
		for i, f := range order {
			dir := "ASC"
			if f.Desc {
				dir = "DESC"
			}
			terms[i] = pgx.Identifier{f.Column}.Sanitize() + " " + dir
		}
		q += " ORDER BY " + strings.Join(terms, ", ")
	}
	if limit > 0 {
		q += fmt.Sprintf(" LIMIT %d OFFSET %d", limit, skip)
	}
	rows, err := s.querier(ctx).Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	docs, err := pgx.CollectRows(rows, pgx.RowToMap)
	if err != nil {
		return nil, err
	}
	return docs, nil
}

// CountDocuments counts rows matching filter.
func (s Store) CountDocuments(ctx context.Context, filter Filter) (int64, error) {
	where, args := whereClause(filter, 0)
	q := fmt.Sprintf("SELECT count(*) FROM %s WHERE %s", s.table(), where)
	var n int64
	if err := s.querier(ctx).QueryRow(ctx, q, args...).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

// FindOne reads one row; pgx.ErrNoRows if none match.
func (s Store) FindOne(ctx context.Context, filter Filter, columns []string) (map[string]any, error) {
	where, args := whereClause(filter, 0)
	q := fmt.Sprintf("SELECT %s FROM %s WHERE %s LIMIT 1", columnList(columns), s.table(), where)
	rows, err := s.querier(ctx).Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return pgx.CollectOneRow(rows, pgx.RowToMap)
}

// InsertMany inserts docs, which must all share the exact same set of
// keys (one INSERT with the columns of docs[0]); a doc with a missing or
// an extra key, relative to docs[0], is rejected rather than silently
// turning a missing key into a NULL column or dropping an extra one.
func (s Store) InsertMany(ctx context.Context, docs []map[string]any) error {
	if len(docs) == 0 {
		return nil
	}
	cols := make([]string, 0, len(docs[0]))
	for col := range docs[0] {
		cols = append(cols, col)
	}
	sort.Strings(cols)
	for i, doc := range docs {
		if len(doc) != len(cols) {
			return fmt.Errorf("postgres: InsertMany: docs[%d] has %d keys, docs[0] has %d", i, len(doc), len(cols))
		}
		for _, col := range cols {
			if _, ok := doc[col]; !ok {
				return fmt.Errorf("postgres: InsertMany: docs[%d] is missing key %q present in docs[0]", i, col)
			}
		}
	}
	placeholders := make([]string, len(cols))
	for i := range cols {
		placeholders[i] = fmt.Sprintf("$%d", i+1)
	}
	q := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)", s.table(), columnList(cols), strings.Join(placeholders, ", "))
	batch := &pgx.Batch{}
	for _, doc := range docs {
		args := make([]any, len(cols))
		for i, col := range cols {
			args[i] = doc[col]
		}
		batch.Queue(q, args...)
	}
	return s.querier(ctx).SendBatch(ctx, batch).Close()
}

// UpdateOne sets the columns in update on at most one row matching filter
// — the row is picked via ctid (Postgres' row identifier), mirroring
// Mongo's "at most one document" semantics instead of SQL's "every
// matching row".
func (s Store) UpdateOne(ctx context.Context, filter, update Filter) (int64, error) {
	if len(update) == 0 {
		return 0, nil
	}
	cols := make([]string, 0, len(update))
	for col := range update {
		cols = append(cols, col)
	}
	sort.Strings(cols)
	sets := make([]string, len(cols))
	args := make([]any, len(cols))
	for i, col := range cols {
		sets[i] = fmt.Sprintf("%s = $%d", pgx.Identifier{col}.Sanitize(), i+1)
		args[i] = update[col]
	}
	where, whereArgs := whereClause(filter, len(cols))
	q := fmt.Sprintf("UPDATE %s SET %s WHERE ctid = (SELECT ctid FROM %s WHERE %s LIMIT 1)",
		s.table(), strings.Join(sets, ", "), s.table(), where)
	tag, err := s.querier(ctx).Exec(ctx, q, append(args, whereArgs...)...)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// ReplaceOne is UpdateOne with every key of replacement as the SET list —
// columns the table has that replacement doesn't are left untouched,
// unlike Mongo's full-document replace. See postgres-rfc.md.
func (s Store) ReplaceOne(ctx context.Context, filter Filter, replacement map[string]any) (int64, error) {
	return s.UpdateOne(ctx, filter, Filter(replacement))
}

// DeleteOne deletes at most one row matching filter (picked via ctid, as
// UpdateOne does).
func (s Store) DeleteOne(ctx context.Context, filter Filter) (int64, error) {
	where, args := whereClause(filter, 0)
	q := fmt.Sprintf("DELETE FROM %s WHERE ctid = (SELECT ctid FROM %s WHERE %s LIMIT 1)",
		s.table(), s.table(), where)
	tag, err := s.querier(ctx).Exec(ctx, q, args...)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
