package postgres

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// maxIdentifierBytes is Postgres' own identifier limit (NAMEDATALEN
// 64, minus the trailing NUL) -- a longer one is silently truncated
// by the server, never rejected, which is exactly the kind of silent
// corruption (two different intended names colliding into the same
// truncated one) this project never accepts (section 7/10.2 of the
// Postgres portability plan's "fail loud" precedent) -- checked here
// instead, before any SQL is sent.
const maxIdentifierBytes = 63

// IndexField is one field of an index's expression. Field is a
// top-level key of the doc jsonb column -- nested/dotted paths are
// out of scope (every index the plan ever needed, sections 4/5/10, is
// a top-level field). Kind, when KindDateTime, wraps the expression
// in hyrule_safe_timestamptz(doc->>field) -- the same function
// query.go's compiler uses for a KindDateTime filter/sort/comparison
// (dateTimeRef) -- so the index indexes the real instant, never the
// stored text. Any other Kind (including the zero value) indexes
// doc->field as-is, the same jsonb-ordering expression section 5's
// unique index already uses; jsonb's own ordering already compares
// numbers/strings/booleans correctly within their own type, so no
// other Kind needs its own cast.
type IndexField struct {
	Field string
	Desc  bool
	Kind  Kind
}

// IndexSpec is one index to ensure exists on Table -- the Postgres
// equivalent of platform/mongodb's IndexSpec (indexes.go), narrower
// on purpose: no TTL/text/hashed/2dsphere (no Postgres equivalent in
// scope here) and no .indices.conf text format (nothing wires a
// Postgres resource into app.Spec to embed one for yet -- see
// EnsureIndexes' own doc comment).
type IndexSpec struct {
	Table string
	// Name defaults to "<table>_<field>[_<field>...]_idx". Checked
	// against maxIdentifierBytes either way -- never silently
	// truncated.
	Name   string
	Fields []IndexField // 1 or more, a compound index in this order
	Unique bool
	// Sparse adds `WHERE doc ? 'field'` (section 5's exact pattern) --
	// only valid with exactly one Field: which field's presence a
	// multi-field WHERE would check is ambiguous, so buildIndexSQL
	// rejects that combination rather than guessing.
	Sparse bool
}

// IndexesMode is what EnsureIndexes does with the missing indexes --
// same names and roles as platform/mongodb's IndexesMode (different
// package, no collision).
type IndexesMode string

const (
	// IndexesCreate builds the missing indexes (the default).
	IndexesCreate IndexesMode = "create"
	// IndexesCheck only logs the missing ones, never creates them.
	IndexesCheck IndexesMode = "check"
	// IndexesOff ignores the declared indexes entirely.
	IndexesOff IndexesMode = "off"
)

// sqlStringLiteral renders s as a single-quoted SQL string literal,
// doubling embedded single quotes the standard, unambiguous way --
// CREATE INDEX can't parameterize the indexed expression itself, so
// Field is baked directly into the DDL text below. Field comes from
// Go code a developer wrote (IndexSpec is built in Go, never decoded
// from client/HTTP input), but escaped properly regardless.
func sqlStringLiteral(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// buildIndexSQL validates spec and returns the index's sanitized name
// and the CREATE INDEX statement that ensures it. The statement is
// IF NOT EXISTS, idempotent on its own -- unlike Mongo, which needs
// EnsureIndexes to list a collection's indexes first because its
// driver has no equivalent -- EnsureIndexes below still checks
// pg_indexes first anyway, so IndexesCheck can report a missing index
// without ever running this statement.
func buildIndexSQL(spec IndexSpec) (name, sql string, err error) {
	if spec.Table == "" {
		return "", "", fmt.Errorf("postgres: IndexSpec.Table must not be empty")
	}
	if len(spec.Fields) == 0 {
		return "", "", fmt.Errorf("postgres: IndexSpec.Fields must not be empty")
	}
	if spec.Sparse && len(spec.Fields) != 1 {
		return "", "", fmt.Errorf("postgres: IndexSpec.Sparse requires exactly one field, got %d", len(spec.Fields))
	}

	exprs := make([]string, len(spec.Fields))
	names := make([]string, len(spec.Fields))
	for i, f := range spec.Fields {
		if f.Field == "" {
			return "", "", fmt.Errorf("postgres: IndexSpec.Fields[%d].Field must not be empty", i)
		}
		// Postgres' CREATE INDEX grammar requires an extra pair of
		// parens around an expression that isn't a bare column name
		// (confirmed by running this for real: ((doc->'slug')) works,
		// (doc->'slug') alone is a syntax error -- the outer parens
		// are the column-list delimiter, the inner pair is what marks
		// this as an expression, same shape section 5's own hardcoded
		// SQL already used).
		expr := fmt.Sprintf("(doc->%s)", sqlStringLiteral(f.Field))
		if f.Kind == KindDateTime {
			expr = fmt.Sprintf("(hyrule_safe_timestamptz(doc->>%s))", sqlStringLiteral(f.Field))
		}
		if f.Desc {
			expr += " DESC"
		}
		exprs[i] = expr
		names[i] = f.Field
	}

	name = spec.Name
	if name == "" {
		name = spec.Table + "_" + strings.Join(names, "_") + "_idx"
	}
	if len(name) > maxIdentifierBytes {
		return "", "", fmt.Errorf("postgres: index name %q is %d bytes, over Postgres' %d-byte identifier limit", name, len(name), maxIdentifierBytes)
	}

	var b strings.Builder
	b.WriteString("CREATE ")
	if spec.Unique {
		b.WriteString("UNIQUE ")
	}
	b.WriteString("INDEX IF NOT EXISTS ")
	b.WriteString(pgx.Identifier{name}.Sanitize())
	b.WriteString(" ON ")
	b.WriteString(pgx.Identifier{spec.Table}.Sanitize())
	b.WriteString(" (")
	b.WriteString(strings.Join(exprs, ", "))
	b.WriteString(")")
	if spec.Sparse {
		b.WriteString(" WHERE doc ? ")
		b.WriteString(sqlStringLiteral(spec.Fields[0].Field))
	}
	return name, b.String(), nil
}

// indexExists reports whether an index named name already exists on
// table, in the current schema -- Postgres' own catalog, no need for
// a round trip through a driver-level list call the way Mongo's
// indexNamesOf needs.
func indexExists(ctx context.Context, pool *pgxpool.Pool, table, name string) (bool, error) {
	var exists bool
	err := pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM pg_indexes WHERE schemaname = current_schema() AND tablename = $1 AND indexname = $2)`,
		table, name).Scan(&exists)
	return exists, err
}

// EnsureIndexes compares the declared indexes with the ones already
// on pool's database and creates the missing ones (IndexesCreate, the
// default), only logs them (IndexesCheck), or does nothing at all
// (IndexesOff) -- mirrors platform/mongodb's EnsureIndexes (same
// three modes, same "missing" return for IndexesCheck), adapted to
// Postgres' own catalog and DDL.
//
// Never called by NewAdapter or any other registration path in this
// repo, same precedent as EnsureFunctions (Fase 1): a schema-setup
// step a caller runs explicitly, alongside CREATE TABLE/CREATE INDEX,
// not something a Resource's own setup triggers on its own. app/app.go
// (the real service bootstrap) has no Postgres integration at all
// today -- there is no Mongo-style .indices.conf/app.Spec.Indexes
// equivalent to wire this into yet; that's separate, larger, unasked-
// for work, not a gap in this function.
//
// Known, accepted limitation: if an index named like one of specs
// already exists with a *different* definition, this treats it as
// already satisfied (IF NOT EXISTS only ever looks at the name) --
// unlike platform/mongodb's EnsureIndexes, which detects that exact
// conflict by the driver's own error codes (85/86) and logs a
// warning. Comparing pg_indexes.indexdef textually against the DDL
// built here would be fragile (Postgres normalizes an expression back
// in a form that doesn't trivially match the input text) and isn't
// attempted -- a reused name with a different definition is an
// operator configuration mistake, not something this function tries
// to detect.
func EnsureIndexes(ctx context.Context, pool *pgxpool.Pool, specs []IndexSpec, mode IndexesMode) ([]IndexSpec, error) {
	if mode == IndexesOff {
		return nil, nil
	}
	type pending struct {
		spec IndexSpec
		name string
		sql  string
	}
	var missing []IndexSpec
	var toCreate []pending
	for _, spec := range specs {
		name, sql, err := buildIndexSQL(spec)
		if err != nil {
			return nil, fmt.Errorf("postgres: EnsureIndexes: %w", err)
		}
		exists, err := indexExists(ctx, pool, spec.Table, name)
		if err != nil {
			return nil, fmt.Errorf("postgres: EnsureIndexes: %w", err)
		}
		if exists {
			continue
		}
		missing = append(missing, spec)
		toCreate = append(toCreate, pending{spec, name, sql})
	}
	if mode == IndexesCheck {
		for _, spec := range missing {
			slog.Warn("postgres: index missing", "table", spec.Table, "fields", spec.Fields)
		}
		return missing, nil
	}
	for _, p := range toCreate {
		if _, err := pool.Exec(ctx, p.sql); err != nil {
			return nil, fmt.Errorf("postgres: EnsureIndexes: create %s.%s: %w", p.spec.Table, p.name, err)
		}
		slog.Info("postgres: index created", "table", p.spec.Table, "index", p.name)
	}
	return nil, nil
}
