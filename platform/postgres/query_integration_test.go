package postgres

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// setupDocTable creates the minimal table query_integration_test.go
// needs against a jsonb "doc" column -- real Postgres, never a mock of
// jsonb semantics.
func setupDocTable(t *testing.T, pool *pgxpool.Pool, table string) {
	ctx := context.Background()
	if _, err := pool.Exec(ctx, "DROP TABLE IF EXISTS "+table); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "CREATE TABLE "+table+" (id SERIAL PRIMARY KEY, doc jsonb NOT NULL)"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pool.Exec(context.Background(), "DROP TABLE IF EXISTS "+table) })
}

func insertDocs(t *testing.T, pool *pgxpool.Pool, table string, docs ...string) {
	ctx := context.Background()
	for _, d := range docs {
		if _, err := pool.Exec(ctx, "INSERT INTO "+table+" (doc) VALUES ($1::jsonb)", d); err != nil {
			t.Fatalf("insert %s: %v", d, err)
		}
	}
}

// namesMatching runs CompileFilter's output against table and returns
// the matched rows' doc->>'name', sorted, for assertion convenience.
func namesMatching(t *testing.T, pool *pgxpool.Pool, table string, q Query, schema SchemaSpec) []string {
	t.Helper()
	where, args, err := CompileFilter(q, schema, 0)
	if err != nil {
		t.Fatalf("CompileFilter: %v", err)
	}
	rows, err := pool.Query(context.Background(), "SELECT doc->>'name' FROM "+table+" WHERE "+where+" ORDER BY doc->>'name'", args...)
	if err != nil {
		t.Fatalf("query (where=%q args=%v): %v", where, args, err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		got = append(got, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return got
}

func assertNames(t *testing.T, got []string, want ...string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestCompileFilterIntegrationEqSemantics(t *testing.T) {
	// $eq:"x" matches only the literal; $eq:null matches only an
	// explicit null, never an absent field -- the named Mongo/Postgres
	// divergence.
	pool := testPool(t)
	table := "query_eq_test"
	setupDocTable(t, pool, table)
	insertDocs(t, pool, table,
		`{"name": "absent"}`,
		`{"name": "nullish", "status": null}`,
		`{"name": "x", "status": "x"}`,
		`{"name": "y", "status": "y"}`,
	)

	assertNames(t, namesMatching(t, pool, table, Query{"status": "x"}, SchemaSpec{}), "x")
	assertNames(t, namesMatching(t, pool, table, Query{"status": Query{"$eq": nil}}, SchemaSpec{}), "nullish")
	// $ne: "x" matches absent and null too (IS DISTINCT FROM is never
	// UNKNOWN), plus any other value -- everything except "x".
	assertNames(t, namesMatching(t, pool, table, Query{"status": Query{"$ne": "x"}}, SchemaSpec{}), "absent", "nullish", "y")
	// $ne: null matches absent too (only an explicit "null" value is
	// excluded) -- confirmed against real Postgres rather than
	// assumed; SQL NULL (an absent key) IS DISTINCT FROM the jsonb
	// value 'null'::jsonb.
	assertNames(t, namesMatching(t, pool, table, Query{"status": Query{"$ne": nil}}, SchemaSpec{}), "absent", "x", "y")
}

func TestCompileFilterIntegrationInNin(t *testing.T) {
	pool := testPool(t)
	table := "query_in_test"
	setupDocTable(t, pool, table)
	insertDocs(t, pool, table,
		`{"name": "a", "status": "draft"}`,
		`{"name": "b", "status": "published"}`,
		`{"name": "c", "status": "archived"}`,
		`{"name": "d"}`,
	)

	assertNames(t, namesMatching(t, pool, table, Query{"status": Query{"$in": []any{"draft", "published"}}}, SchemaSpec{}), "a", "b")
	assertNames(t, namesMatching(t, pool, table, Query{"status": Query{"$in": []any{}}}, SchemaSpec{}))
	assertNames(t, namesMatching(t, pool, table, Query{"status": Query{"$nin": []any{}}}, SchemaSpec{}), "a", "b", "c", "d")
}

func TestCompileFilterIntegrationComparisonWithKind(t *testing.T) {
	pool := testPool(t)
	table := "query_cmp_test"
	setupDocTable(t, pool, table)
	insertDocs(t, pool, table,
		`{"name": "low", "views": 5}`,
		`{"name": "mid", "views": 50}`,
		`{"name": "high", "views": 500}`,
	)
	schema := SchemaSpec{Fields: map[string]Kind{"views": KindInteger}}

	assertNames(t, namesMatching(t, pool, table, Query{"views": Query{"$gt": 10}}, schema), "high", "mid")
	assertNames(t, namesMatching(t, pool, table, Query{"views": Query{"$gte": 50, "$lte": 500}}, schema), "high", "mid")
}

func TestCompileFilterIntegrationComparisonWithoutDeclaredKindAlwaysMatches(t *testing.T) {
	// Without a Kind declared for "views", the filter value casts to
	// jsonb text (SchemaSpec's default), never jsonb number. Confirmed
	// directly against Postgres (not assumed): a jsonb number always
	// ranks above a jsonb string in Postgres' cross-type ordering
	// (`SELECT '9'::jsonb > '"100"'::jsonb` is true), so $gt on a
	// numeric field compared against a text-cast literal matches every
	// row, regardless of either value -- a vacuous, meaningless
	// result. This is why schema.Fields must declare the field's real
	// Kind (see TestCompileFilterIntegrationComparisonWithKind).
	pool := testPool(t)
	table := "query_cmp_untyped_test"
	setupDocTable(t, pool, table)
	insertDocs(t, pool, table, `{"name": "nine", "views": 9}`, `{"name": "fivehundred", "views": 500}`)

	got := namesMatching(t, pool, table, Query{"views": Query{"$gt": "100"}}, SchemaSpec{})
	assertNames(t, got, "fivehundred", "nine")
}

func TestCompileFilterIntegrationHasFamily(t *testing.T) {
	pool := testPool(t)
	table := "query_has_test"
	setupDocTable(t, pool, table)
	insertDocs(t, pool, table,
		`{"name": "a", "tags": ["go", "rest"]}`,
		`{"name": "b", "tags": ["go"]}`,
		`{"name": "c", "tags": []}`,
		`{"name": "d", "tags": null}`,
		`{"name": "e"}`,
	)

	assertNames(t, namesMatching(t, pool, table, Query{"tags": Query{"$has": "go"}}, SchemaSpec{}), "a", "b")
	assertNames(t, namesMatching(t, pool, table, Query{"tags": Query{"$hasSome": []any{"rest", "nope"}}}, SchemaSpec{}), "a")
	assertNames(t, namesMatching(t, pool, table, Query{"tags": Query{"$hasEvery": []any{"go", "rest"}}}, SchemaSpec{}), "a")
	// A null element in the $hasEvery list -- no row has a literal null
	// in tags, so this is expected to match nothing.
	assertNames(t, namesMatching(t, pool, table, Query{"tags": Query{"$hasEvery": []any{nil}}}, SchemaSpec{}))
	// $has/$hasSome/$hasEvery on a non-array (null, missing) never
	// match -- the CASE's ELSE FALSE branch.
	assertNames(t, namesMatching(t, pool, table, Query{"tags": Query{"$has": "go"}}, SchemaSpec{}), "a", "b")
}

func TestCompileFilterIntegrationHasFamilyUsesElementKindNotFieldKind(t *testing.T) {
	// A KindArray field's search value must cast using
	// SchemaSpec.ElementKinds, not Fields[field] (KindArray itself) --
	// before the fix, $has/$hasSome/$hasEvery always cast the search
	// value as text (KindArray/KindObject fall through SQLCast's
	// default), so a numeric element search silently matched nothing.
	pool := testPool(t)
	table := "query_has_element_kind_test"
	setupDocTable(t, pool, table)
	insertDocs(t, pool, table,
		`{"name": "a", "scores": [1, 5, 9]}`,
		`{"name": "b", "scores": [2, 4]}`,
	)
	schema := SchemaSpec{
		Fields:       map[string]Kind{"scores": KindArray},
		ElementKinds: map[string]Kind{"scores": KindInteger},
	}
	assertNames(t, namesMatching(t, pool, table, Query{"scores": Query{"$has": 5}}, schema), "a")
	assertNames(t, namesMatching(t, pool, table, Query{"scores": Query{"$hasSome": []any{4, 100}}}, schema), "b")
	assertNames(t, namesMatching(t, pool, table, Query{"scores": Query{"$hasEvery": []any{1, 9}}}, schema), "a")
}

func TestCompileFilterIntegrationNestedPath(t *testing.T) {
	pool := testPool(t)
	table := "query_nested_test"
	setupDocTable(t, pool, table)
	insertDocs(t, pool, table,
		`{"name": "a", "meta": {"author": "link"}}`,
		`{"name": "b", "meta": {"author": "zelda"}}`,
		`{"name": "c"}`,
	)

	assertNames(t, namesMatching(t, pool, table, Query{"meta.author": Query{"$eq": "link"}}, SchemaSpec{}), "a")
	assertNames(t, namesMatching(t, pool, table, Query{"meta.author": Query{"$exists": true}}, SchemaSpec{}), "a", "b")
	assertNames(t, namesMatching(t, pool, table, Query{"meta.author": Query{"$exists": false}}, SchemaSpec{}), "c")
}

func TestCompileFilterIntegrationNotAndOr(t *testing.T) {
	pool := testPool(t)
	table := "query_boolean_test"
	setupDocTable(t, pool, table)
	insertDocs(t, pool, table,
		`{"name": "a", "views": 150}`,
		`{"name": "b", "views": 50}`,
		`{"name": "c"}`,
		`{"name": "d", "views": "N/D"}`,
	)
	schema := SchemaSpec{Fields: map[string]Kind{"views": KindInteger}}

	// $not on views > 100: matches every row where the leaf isn't
	// TRUE, including absent/non-numeric.
	assertNames(t, namesMatching(t, pool, table, Query{"$not": Query{"views": Query{"$gt": 100}}}, schema), "b", "c", "d")
	assertNames(t, namesMatching(t, pool, table, Query{"$or": []Query{{"views": Query{"$gt": 100}}, {"name": "c"}}}, schema), "a", "c")
}

func TestCompileFilterIntegrationNotOfAndOr(t *testing.T) {
	// $not wrapping a composite ($and/$or), not just a single leaf --
	// each leaf inside is already IS TRUE-normalized (never NULL), so
	// NOT(...) of the whole composite is well-defined too.
	pool := testPool(t)
	table := "query_not_composite_test"
	setupDocTable(t, pool, table)
	insertDocs(t, pool, table,
		`{"name": "a", "status": "published", "views": 150}`,
		`{"name": "b", "status": "published", "views": 50}`,
		`{"name": "c", "status": "draft", "views": 150}`,
		`{"name": "d"}`,
	)
	schema := SchemaSpec{Fields: map[string]Kind{"views": KindInteger}}

	// NOT(status=published AND views>100): everything except "a".
	assertNames(t, namesMatching(t, pool, table, Query{"$not": Query{"$and": []Query{
		{"status": "published"}, {"views": Query{"$gt": 100}},
	}}}, schema), "b", "c", "d")

	// NOT(NOT(status=published)): double negation round-trips back to
	// the plain match -- "a" and "b" only.
	assertNames(t, namesMatching(t, pool, table, Query{"$not": Query{"$not": Query{"status": "published"}}}, schema), "a", "b")
}

func TestCompileFilterIntegrationDateTimeComparisons(t *testing.T) {
	// KindDateTime compares by real timestamptz instant, via
	// hyrule_safe_timestamptz(doc->>field) -- never tested against real
	// Postgres before this revision. EnsureFunctions is this package's
	// own schema-setup step (platform/postgres/functions.go), a
	// precondition for any KindDateTime filter, same as the table/index
	// DDL setupDocTable already runs.
	pool := testPool(t)
	if err := EnsureFunctions(context.Background(), pool); err != nil {
		t.Fatal(err)
	}
	table := "query_datetime_test"
	setupDocTable(t, pool, table)
	insertDocs(t, pool, table,
		`{"name": "old", "published_at": "2020-01-01T00:00:00Z"}`,
		`{"name": "mid", "published_at": "2023-06-15T12:00:00Z"}`,
		`{"name": "new", "published_at": "2026-01-01T00:00:00Z"}`,
		`{"name": "absent"}`,
		`{"name": "nullish", "published_at": null}`,
		`{"name": "garbage", "published_at": "not-a-date"}`,
	)
	schema := SchemaSpec{Fields: map[string]Kind{"published_at": KindDateTime}}

	// Comparison is by real instant, via hyrule_safe_timestamptz --
	// never by jsonb text. Confirmed against real Postgres: an earlier
	// version of this test, before query.go's fix, used to_jsonb(...)
	// text comparison instead, which silently returned wrong results
	// for $eq (to_jsonb always renders "+00:00", never "Z", so a value
	// stored with "Z" never textually matched even the exact same
	// instant) and relied on luck for $gt/$gte/$lt/$lte boundaries.
	assertNames(t, namesMatching(t, pool, table, Query{"published_at": Query{"$gt": "2021-01-01T00:00:00Z"}}, schema), "mid", "new")
	assertNames(t, namesMatching(t, pool, table, Query{"published_at": Query{"$gte": "2023-06-15T12:00:00Z", "$lte": "2024-01-01T00:00:00Z"}}, schema), "mid")
	assertNames(t, namesMatching(t, pool, table, Query{"published_at": Query{"$lt": "2021-01-01T00:00:00Z"}}, schema), "old")
	// $eq now matches by instant: the exact same timestamp, written
	// with the "Z" suffix clients actually use, matches correctly.
	assertNames(t, namesMatching(t, pool, table, Query{"published_at": Query{"$eq": "2020-01-01T00:00:00Z"}}, schema), "old")
	// absent/null/unparseable text never satisfy any comparison --
	// hyrule_safe_timestamptz returns SQL NULL for all three, which IS
	// TRUE always turns into FALSE, never a cross-type-ordering
	// accident.
	assertNames(t, namesMatching(t, pool, table, Query{"published_at": Query{"$gt": "1900-01-01T00:00:00Z"}}, schema), "mid", "new", "old")
	assertNames(t, namesMatching(t, pool, table, Query{"published_at": Query{"$eq": nil}}, schema), "nullish")
}

func TestCompileFilterIntegrationDateTimeInNin(t *testing.T) {
	// $in/$nin on a KindDateTime field must compare by instant too --
	// found missing, in code review of this same revision's $eq/$ne
	// fix, before any test exercised it: $in silently matched nothing
	// for the exact same instant, the same bug $eq had.
	pool := testPool(t)
	if err := EnsureFunctions(context.Background(), pool); err != nil {
		t.Fatal(err)
	}
	table := "query_datetime_in_test"
	setupDocTable(t, pool, table)
	insertDocs(t, pool, table,
		`{"name": "old", "published_at": "2020-01-01T00:00:00Z"}`,
		`{"name": "mid", "published_at": "2023-06-15T12:00:00Z"}`,
		`{"name": "new", "published_at": "2026-01-01T00:00:00Z"}`,
		`{"name": "absent"}`,
		`{"name": "nullish", "published_at": null}`,
	)
	schema := SchemaSpec{Fields: map[string]Kind{"published_at": KindDateTime}}

	// The "Z"-suffixed literal must match the "Z"-suffixed stored value
	// -- before the fix, this matched nothing (to_jsonb's "+00:00"
	// never equals the stored "Z" text).
	assertNames(t, namesMatching(t, pool, table, Query{"published_at": Query{"$in": []any{"2020-01-01T00:00:00Z", "2026-01-01T00:00:00Z"}}}, schema), "new", "old")
	assertNames(t, namesMatching(t, pool, table, Query{"published_at": Query{"$nin": []any{"2020-01-01T00:00:00Z", "2026-01-01T00:00:00Z"}}}, schema), "absent", "mid", "nullish")
	// An explicit nil inside the list matches the explicit-null row,
	// via dateTimeLiteral's NULL::timestamptz and IS NOT DISTINCT FROM
	// -- never the absent row (hyrule_safe_timestamptz(doc->>field) is
	// SQL NULL for both, but IS NOT DISTINCT FROM NULL still matches an
	// absent field too, same as TestCompileFilterIntegrationInNinWithNullAndAbsent's
	// finding for the untyped path -- confirmed, not assumed).
	assertNames(t, namesMatching(t, pool, table, Query{"published_at": Query{"$in": []any{nil}}}, schema), "absent", "nullish")
}

func TestCompileFilterDateTimeHasFamilyUnsupported(t *testing.T) {
	// $has/$hasSome/$hasEvery on a KindDateTime element fail loudly at
	// compile time, rather than silently reusing jsonb containment's
	// text comparison (the same bug this revision fixed for $eq/$ne) --
	// no instant-aware equivalent of @> exists yet.
	schema := SchemaSpec{
		Fields:       map[string]Kind{"dates": KindArray},
		ElementKinds: map[string]Kind{"dates": KindDateTime},
	}
	cases := []Query{
		{"dates": Query{"$has": "2020-01-01T00:00:00Z"}},
		{"dates": Query{"$hasSome": []any{"2020-01-01T00:00:00Z"}}},
		{"dates": Query{"$hasEvery": []any{"2020-01-01T00:00:00Z"}}},
	}
	for _, q := range cases {
		if _, _, err := CompileFilter(q, schema, 0); err == nil {
			t.Errorf("%v: expected an error, got none", q)
		}
	}
}

func TestCompileFilterIntegrationNumberKindDecimal(t *testing.T) {
	// KindNumber ("numeric" cast) with non-integer values -- only
	// KindInteger was exercised against real Postgres before this
	// revision.
	pool := testPool(t)
	table := "query_number_test"
	setupDocTable(t, pool, table)
	insertDocs(t, pool, table,
		`{"name": "cheap", "price": 9.99}`,
		`{"name": "mid", "price": 49.5}`,
		`{"name": "pricey", "price": 199.0}`,
	)
	schema := SchemaSpec{Fields: map[string]Kind{"price": KindNumber}}

	assertNames(t, namesMatching(t, pool, table, Query{"price": Query{"$gt": 10.0}}, schema), "mid", "pricey")
	assertNames(t, namesMatching(t, pool, table, Query{"price": Query{"$eq": 49.5}}, schema), "mid")
}

func TestCompileFilterIntegrationBooleanKind(t *testing.T) {
	pool := testPool(t)
	table := "query_bool_test"
	setupDocTable(t, pool, table)
	insertDocs(t, pool, table,
		`{"name": "a", "featured": true}`,
		`{"name": "b", "featured": false}`,
		`{"name": "c"}`,
	)
	schema := SchemaSpec{Fields: map[string]Kind{"featured": KindBoolean}}

	assertNames(t, namesMatching(t, pool, table, Query{"featured": Query{"$eq": true}}, schema), "a")
	assertNames(t, namesMatching(t, pool, table, Query{"featured": Query{"$ne": true}}, schema), "b", "c")
}

func TestCompileFilterIntegrationInNinWithNullAndAbsent(t *testing.T) {
	// $in/$nin with an explicit null inside the list, against rows that
	// are absent/null/a real value -- not covered before this revision
	// (only a non-null, non-empty list was tested against real
	// Postgres).
	pool := testPool(t)
	table := "query_in_null_test"
	setupDocTable(t, pool, table)
	insertDocs(t, pool, table,
		`{"name": "absent"}`,
		`{"name": "nullish", "status": null}`,
		`{"name": "x", "status": "x"}`,
		`{"name": "y", "status": "y"}`,
	)

	// $in: [null, "x"] -- IS NOT DISTINCT FROM matches both the
	// explicit null and "x", but never an absent key (SQL NULL from the
	// -> operator, which IS NOT DISTINCT FROM 'null'::jsonb is false).
	assertNames(t, namesMatching(t, pool, table, Query{"status": Query{"$in": []any{nil, "x"}}}, SchemaSpec{}), "nullish", "x")
	// $nin: [null, "x"] -- everything else, including "absent" (its SQL
	// NULL doc->'status' IS NOT DISTINCT FROM neither list element).
	assertNames(t, namesMatching(t, pool, table, Query{"status": Query{"$nin": []any{nil, "x"}}}, SchemaSpec{}), "absent", "y")
}

func TestCompileFilterIntegrationNestedPathNe(t *testing.T) {
	// $ne on a nested path -- only $eq/$exists were covered before this
	// revision.
	pool := testPool(t)
	table := "query_nested_ne_test"
	setupDocTable(t, pool, table)
	insertDocs(t, pool, table,
		`{"name": "a", "meta": {"author": "link"}}`,
		`{"name": "b", "meta": {"author": "zelda"}}`,
		`{"name": "c"}`,
		`{"name": "d", "meta": {"author": null}}`,
	)

	// IS DISTINCT FROM is never UNKNOWN: absent (no "meta" key at all,
	// doc #> {meta,author} is SQL NULL) and explicit null both count as
	// "distinct from link".
	assertNames(t, namesMatching(t, pool, table, Query{"meta.author": Query{"$ne": "link"}}, SchemaSpec{}), "b", "c", "d")
}

// TestCompileFilterIntegrationExplainUsesIndex documents, rather than
// strictly requires (per plan section 20's Fase 3 scope: "sem exigir
// Index Scan em todo cenário"), the real query plan Postgres picks for
// a selective $eq filter on a field covered by the section 5 sparse
// unique index. Correctness (the right row comes back) is still
// asserted unconditionally.
func TestCompileFilterIntegrationExplainUsesIndex(t *testing.T) {
	pool := testPool(t)
	table := "query_explain_index_test"
	ctx := context.Background()
	if _, err := pool.Exec(ctx, "DROP TABLE IF EXISTS "+table); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "CREATE TABLE "+table+" (id SERIAL PRIMARY KEY, doc jsonb NOT NULL)"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pool.Exec(context.Background(), "DROP TABLE IF EXISTS "+table) })
	// Section 5's sparse unique index: only rows with a "slug" key
	// participate.
	if _, err := pool.Exec(ctx, "CREATE UNIQUE INDEX ON "+table+" ((doc->'slug')) WHERE doc ? 'slug'"); err != nil {
		t.Fatal(err)
	}

	const n = 5000
	docs := make([]string, n)
	for i := range docs {
		docs[i] = fmt.Sprintf(`{"name": "row-%d", "slug": "slug-%d"}`, i, i)
	}
	insertDocs(t, pool, table, docs...)
	if err := pool.QueryRow(ctx, "ANALYZE "+table).Scan(); err != nil && err.Error() != "no rows in result set" {
		t.Fatal(err)
	}

	schema := SchemaSpec{}
	where, args, err := CompileFilter(Query{"slug": "slug-2500"}, schema, 0)
	if err != nil {
		t.Fatal(err)
	}
	assertNames(t, namesMatching(t, pool, table, Query{"slug": "slug-2500"}, schema), "row-2500")

	rows, err := pool.Query(ctx, "EXPLAIN (ANALYZE, BUFFERS) SELECT doc->>'name' FROM "+table+" WHERE "+where, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan strings.Builder
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatal(err)
		}
		plan.WriteString(line)
		plan.WriteString("\n")
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	t.Logf("EXPLAIN (ANALYZE, BUFFERS) for %q on %d rows:\n%s", where, n, plan.String())
	if !strings.Contains(plan.String(), "Index") {
		t.Logf("note: planner did not pick an index-based plan for this run -- not asserted as a failure, per plan section 20")
	}
}
