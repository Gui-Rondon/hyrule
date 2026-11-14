package restpostgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Gui-Rondon/hyrule/platform/postgres"
	"github.com/Gui-Rondon/hyrule/rest"
)

// postgresLookup is the CAS lookup WriteLookup builds: id = ID AND
// version = Version. FindOne/UpdateOne/ReplaceOne/DeleteOne also
// accept a bare uuid.UUID lookup (id = value, no version check), for a
// plain "find/write by id" with no optimistic-concurrency condition.
type postgresLookup struct {
	ID      any // uuid.UUID
	Version int64
}

// equalsFilter is what EqualsFilter returns: a jsonb field equals
// value, optionally excluding one document by id -- id isn't a doc
// field, so this can't be expressed as a plain postgres.Query.
type equalsFilter struct {
	query     postgres.Query
	excludeID *uuid.UUID
}

// compileFilter renders filter as a boolean SQL expression, starting
// placeholders at startAt (as postgres.CompileFilter's own startAt
// does). filter is nil (matches every row), a postgres.Query, an
// equalsFilter (EqualsFilter's result), or a combinedFilter
// (CombineFilter's result, query.go).
func (a *postgresAdapter) compileFilter(filter any, startAt int) (string, []any, error) {
	switch f := filter.(type) {
	case nil:
		return "TRUE", nil, nil
	case postgres.Query:
		return postgres.CompileFilter(f, a.schema, startAt)
	case equalsFilter:
		where, args, err := postgres.CompileFilter(f.query, a.schema, startAt)
		if err != nil {
			return "", nil, err
		}
		if f.excludeID == nil {
			return where, args, nil
		}
		args = append(args, f.excludeID.String())
		return fmt.Sprintf("(%s) AND id != $%d::uuid", where, startAt+len(args)), args, nil
	case combinedFilter:
		where, args, err := postgres.CompileFilter(f.query, a.schema, startAt)
		if err != nil {
			return "", nil, err
		}
		if !f.excludeDeleted {
			return where, args, nil
		}
		// "deleted" is a physical column, never part of the jsonb doc --
		// CompileFilter has no way to express this, so it's ANDed in
		// here as raw SQL instead, no bind parameter needed (a literal
		// boolean, never client input).
		return fmt.Sprintf("(%s) AND deleted = false", where), args, nil
	default:
		return "", nil, fmt.Errorf("restpostgres: filter must be a postgres.Query, EqualsFilter's result, or CombineFilter's result, got %T", filter)
	}
}

// lookupWhere renders lookup (a uuid.UUID or a postgresLookup) as a
// boolean SQL expression over the physical id/version columns,
// starting placeholders at startAt.
func (a *postgresAdapter) lookupWhere(lookup any, startAt int) (string, []any, error) {
	switch l := lookup.(type) {
	case uuid.UUID:
		return fmt.Sprintf("id = $%d::uuid", startAt+1), []any{l.String()}, nil
	case postgresLookup:
		id, ok := l.ID.(uuid.UUID)
		if !ok {
			return "", nil, fmt.Errorf("restpostgres: lookup.ID must be a uuid.UUID, got %T", l.ID)
		}
		return fmt.Sprintf("id = $%d::uuid AND version = $%d", startAt+1, startAt+2), []any{id.String(), l.Version}, nil
	default:
		return "", nil, fmt.Errorf("restpostgres: unsupported lookup type %T", lookup)
	}
}

const selectColumns = "id::text, doc, created_at, updated_at, version, deleted"

func (a *postgresAdapter) scanDoc(row pgx.Rows) (rest.Doc, error) {
	var idText string
	var fields map[string]any
	var createdAt, updatedAt time.Time
	var version int64
	var deleted bool
	if err := row.Scan(&idText, &fields, &createdAt, &updatedAt, &version, &deleted); err != nil {
		return nil, err
	}
	id, err := uuid.Parse(idText)
	if err != nil {
		return nil, fmt.Errorf("restpostgres: stored id %q is not a valid uuid: %w", idText, err)
	}
	return a.hydrate(id, fields, createdAt, updatedAt, version, deleted), nil
}

// Find lists documents matching filter; limit <= 0 means no
// LIMIT/OFFSET (skip is then ignored too, matching platform/postgres.
// Store.Find's documented behavior). Postgres always hydrates the full
// Doc from the row before applying projection -- see Projection's doc
// comment on why.
func (a *postgresAdapter) Find(ctx context.Context, filter, sort, projection any, skip, limit int64) ([]rest.Doc, error) {
	where, args, err := a.compileFilter(filter, 0)
	if err != nil {
		return nil, err
	}
	sortTerms, err := toSortTerms(sort)
	if err != nil {
		return nil, err
	}
	proj, err := toProjection(projection)
	if err != nil {
		return nil, err
	}
	orderBy, sortArgs := a.orderByClause(sortTerms, len(args))
	args = append(args, sortArgs...)

	q := fmt.Sprintf("SELECT %s FROM %s WHERE %s%s", selectColumns, a.table, where, orderBy)
	if limit > 0 {
		q += fmt.Sprintf(" LIMIT %d OFFSET %d", limit, skip)
	}
	rows, err := a.store.Querier(ctx).Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var docs []rest.Doc
	for rows.Next() {
		doc, err := a.scanDoc(rows)
		if err != nil {
			return nil, err
		}
		docs = append(docs, applyProjection(doc, proj))
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return docs, nil
}

// CountDocuments counts rows matching filter.
func (a *postgresAdapter) CountDocuments(ctx context.Context, filter any) (int64, error) {
	where, args, err := a.compileFilter(filter, 0)
	if err != nil {
		return 0, err
	}
	q := fmt.Sprintf("SELECT count(*) FROM %s WHERE %s", a.table, where)
	var n int64
	if err := a.store.Querier(ctx).QueryRow(ctx, q, args...).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

// FindOne reads one document by lookup; pgx.ErrNoRows if none match.
func (a *postgresAdapter) FindOne(ctx context.Context, lookup, projection any) (rest.Doc, error) {
	where, args, err := a.lookupWhere(lookup, 0)
	if err != nil {
		return nil, err
	}
	proj, err := toProjection(projection)
	if err != nil {
		return nil, err
	}
	q := fmt.Sprintf("SELECT %s FROM %s WHERE %s LIMIT 1", selectColumns, a.table, where)
	rows, err := a.store.Querier(ctx).Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return nil, err
		}
		return nil, pgx.ErrNoRows
	}
	doc, err := a.scanDoc(rows)
	if err != nil {
		return nil, err
	}
	return applyProjection(doc, proj), nil
}

// InsertMany inserts docs in one statement (atomic by construction --
// all rows or none). Each doc must already carry _id/_created/_updated
// (Service.Insert sets these before hooks run, per postgres-rfc.md §6)
// -- InsertMany reads them, never recomputes them, and sets _etag to
// "1" (the only metadata it defines itself): every new document starts
// at version 1. No RETURNING is needed -- Go already knows, with
// certainty, the values it just sent as literal parameters.
func (a *postgresAdapter) InsertMany(ctx context.Context, docs []rest.Doc) error {
	if len(docs) == 0 {
		return nil
	}
	values := make([]string, len(docs))
	args := make([]any, 0, len(docs)*4)
	for i, doc := range docs {
		id, err := requiredMeta[uuid.UUID](doc, "_id", i)
		if err != nil {
			return err
		}
		createdAt, err := requiredMeta[time.Time](doc, "_created", i)
		if err != nil {
			return err
		}
		updatedAt, err := requiredMeta[time.Time](doc, "_updated", i)
		if err != nil {
			return err
		}
		fieldsJSON, err := json.Marshal(businessFieldsOnly(doc))
		if err != nil {
			return fmt.Errorf("restpostgres: InsertMany: docs[%d]: %w", i, err)
		}
		n := len(args)
		values[i] = fmt.Sprintf("($%d::uuid, $%d::jsonb, $%d, $%d, 1)", n+1, n+2, n+3, n+4)
		args = append(args, id.String(), fieldsJSON, createdAt, updatedAt)
		doc["_etag"] = "1"
	}
	q := fmt.Sprintf("INSERT INTO %s (id, doc, created_at, updated_at, version) VALUES %s", a.table, strings.Join(values, ", "))
	_, err := a.store.Querier(ctx).Exec(ctx, q, args...)
	return err
}

// UpdateOne overwrites the whole doc column at lookup (the CAS
// condition WriteLookup built, or a bare id with no version check) and
// bumps version/updated_at in the same statement -- never a partial
// $set, since doc is an opaque jsonb blob here. On success (matched ==
// 1), updated["_etag"]/["_updated"] are set from what RETURNING
// confirms the database actually wrote. changes is accepted for
// interface parity with mongoAdapter (which needs it to build a $set)
// but unused here. matched == 0 without an error is not a failure --
// the caller (checkMatched) decides 404 vs 412 by re-reading.
func (a *postgresAdapter) UpdateOne(ctx context.Context, lookup any, updated, changes rest.Doc) (int64, error) {
	where, whereArgs, err := a.lookupWhere(lookup, 1)
	if err != nil {
		return 0, err
	}
	fieldsJSON, err := json.Marshal(businessFieldsOnly(updated))
	if err != nil {
		return 0, fmt.Errorf("restpostgres: UpdateOne: %w", err)
	}
	args := append([]any{fieldsJSON}, whereArgs...)
	q := fmt.Sprintf("UPDATE %s SET doc = $1::jsonb, version = version + 1, updated_at = now() WHERE %s RETURNING version, updated_at", a.table, where)
	var version int64
	var updatedAt time.Time
	if err := a.store.Querier(ctx).QueryRow(ctx, q, args...).Scan(&version, &updatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, nil
		}
		return 0, err
	}
	updated["_etag"] = strconv.FormatInt(version, 10)
	updated["_updated"] = updatedAt
	return 1, nil
}

// ReplaceOne is UpdateOne under another name: a full-document jsonb
// overwrite is already what UpdateOne does, so a PUT (no merge, "last
// writer wins") needs no SQL of its own.
func (a *postgresAdapter) ReplaceOne(ctx context.Context, lookup any, replacement rest.Doc) (int64, error) {
	return a.UpdateOne(ctx, lookup, replacement, nil)
}

// DeleteOne deletes the row at lookup. Hard delete only -- soft delete
// (setting the deleted column) is Fase 4 (rest.Service integration).
func (a *postgresAdapter) DeleteOne(ctx context.Context, lookup any) (int64, error) {
	where, args, err := a.lookupWhere(lookup, 0)
	if err != nil {
		return 0, err
	}
	q := fmt.Sprintf("DELETE FROM %s WHERE %s", a.table, where)
	tag, err := a.store.Querier(ctx).Exec(ctx, q, args...)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
