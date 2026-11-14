package postgres

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func TestBuildIndexSQLValidation(t *testing.T) {
	cases := []struct {
		name string
		spec IndexSpec
	}{
		{"empty table", IndexSpec{Fields: []IndexField{{Field: "slug"}}}},
		{"empty fields", IndexSpec{Table: "articles"}},
		{"sparse with two fields", IndexSpec{Table: "articles", Sparse: true,
			Fields: []IndexField{{Field: "a"}, {Field: "b"}}}},
		{"empty field name", IndexSpec{Table: "articles", Fields: []IndexField{{Field: ""}}}},
		{"name over the identifier limit", IndexSpec{Table: "articles",
			Fields: []IndexField{{Field: "slug"}}, Name: strings.Repeat("x", maxIdentifierBytes+1)}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, _, err := buildIndexSQL(c.spec); err == nil {
				t.Errorf("buildIndexSQL(%+v) = nil error, want one", c.spec)
			}
		})
	}
}

func TestBuildIndexSQLDefaultName(t *testing.T) {
	name, sql, err := buildIndexSQL(IndexSpec{Table: "articles", Fields: []IndexField{{Field: "slug"}}})
	if err != nil {
		t.Fatal(err)
	}
	if name != "articles_slug_idx" {
		t.Errorf("name = %q, want articles_slug_idx", name)
	}
	if !strings.Contains(sql, `doc->'slug'`) {
		t.Errorf("sql = %q, want doc->'slug'", sql)
	}

	compoundName, _, err := buildIndexSQL(IndexSpec{Table: "articles",
		Fields: []IndexField{{Field: "a"}, {Field: "b", Desc: true}}})
	if err != nil {
		t.Fatal(err)
	}
	if compoundName != "articles_a_b_idx" {
		t.Errorf("compound name = %q, want articles_a_b_idx", compoundName)
	}
}

func TestBuildIndexSQLExplicitNameAndUnique(t *testing.T) {
	name, sql, err := buildIndexSQL(IndexSpec{
		Table: "articles", Name: "by_slug", Unique: true,
		Fields: []IndexField{{Field: "slug"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if name != "by_slug" {
		t.Errorf("name = %q, want by_slug", name)
	}
	if !strings.Contains(sql, "CREATE UNIQUE INDEX IF NOT EXISTS") {
		t.Errorf("sql = %q, want a UNIQUE INDEX IF NOT EXISTS", sql)
	}
}

// TestBuildIndexSQLSparse locks in section 5's exact pattern: the
// sparse condition is on the one field a sparse index is allowed to
// have (validated by TestBuildIndexSQLValidation's "sparse with two
// fields" case).
func TestBuildIndexSQLSparse(t *testing.T) {
	_, sql, err := buildIndexSQL(IndexSpec{Table: "articles", Sparse: true,
		Fields: []IndexField{{Field: "slug"}}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sql, `WHERE doc ? 'slug'`) {
		t.Errorf("sql = %q, want a WHERE doc ? 'slug' clause", sql)
	}
}

// TestBuildIndexSQLDateTimeKind confirms the expression matches
// query.go's own dateTimeRef exactly (doc->>field, not doc->field,
// wrapped in hyrule_safe_timestamptz) -- the whole point of a
// KindDateTime index is comparing by real instant, same as a
// KindDateTime filter/sort already does (section 10.2).
func TestBuildIndexSQLDateTimeKind(t *testing.T) {
	_, sql, err := buildIndexSQL(IndexSpec{Table: "articles",
		Fields: []IndexField{{Field: "published_at", Kind: KindDateTime}}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sql, `hyrule_safe_timestamptz(doc->>'published_at')`) {
		t.Errorf("sql = %q, want hyrule_safe_timestamptz(doc->>'published_at')", sql)
	}
}

func TestBuildIndexSQLEscapesFieldNames(t *testing.T) {
	_, sql, err := buildIndexSQL(IndexSpec{Table: "articles",
		Fields: []IndexField{{Field: "a'b"}}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sql, `doc->'a''b'`) {
		t.Errorf("sql = %q, want the embedded quote doubled", sql)
	}
}

func TestEnsureIndexesCreateIsIdempotentAndCheckNeverCreates(t *testing.T) {
	pool := testPool(t)
	table := "indexes_create_test"
	setupDocTable(t, pool, table)
	spec := IndexSpec{Table: table, Unique: true, Sparse: true, Fields: []IndexField{{Field: "slug"}}}

	// IndexesCheck: reports it as missing, creates nothing.
	missing, err := EnsureIndexes(context.Background(), pool, []IndexSpec{spec}, IndexesCheck)
	if err != nil {
		t.Fatal(err)
	}
	if len(missing) != 1 {
		t.Fatalf("missing = %v, want exactly the one spec", missing)
	}
	if exists, err := indexExists(context.Background(), pool, table, "indexes_create_test_slug_idx"); err != nil || exists {
		t.Fatalf("exists = %v, err = %v, want false (IndexesCheck must never create)", exists, err)
	}

	// IndexesOff: does nothing either.
	if _, err := EnsureIndexes(context.Background(), pool, []IndexSpec{spec}, IndexesOff); err != nil {
		t.Fatal(err)
	}
	if exists, err := indexExists(context.Background(), pool, table, "indexes_create_test_slug_idx"); err != nil || exists {
		t.Fatalf("exists = %v, err = %v, want false (IndexesOff must never create)", exists, err)
	}

	// IndexesCreate: actually creates it.
	if _, err := EnsureIndexes(context.Background(), pool, []IndexSpec{spec}, IndexesCreate); err != nil {
		t.Fatal(err)
	}
	exists, err := indexExists(context.Background(), pool, table, "indexes_create_test_slug_idx")
	if err != nil {
		t.Fatal(err)
	}
	if !exists {
		t.Fatal("index was not created")
	}

	// Idempotent: running it again must not error (IF NOT EXISTS), and
	// a second real unique document doesn't collide with the index
	// -- insert one row with the slug, confirm the real guarantee
	// (the index), not just this function's own bookkeeping.
	if _, err := EnsureIndexes(context.Background(), pool, []IndexSpec{spec}, IndexesCreate); err != nil {
		t.Fatalf("second EnsureIndexes call (IF NOT EXISTS) must not fail: %v", err)
	}
	insertDocs(t, pool, table, `{"name": "a", "slug": "hello"}`)
	if _, err := pool.Exec(context.Background(), fmt.Sprintf(`INSERT INTO %s (doc) VALUES ('{"name": "b", "slug": "hello"}'::jsonb)`, table)); err == nil {
		t.Fatal("expected a unique violation inserting a second document with the same slug")
	}

	// Now that it exists, IndexesCheck reports nothing missing.
	missing, err = EnsureIndexes(context.Background(), pool, []IndexSpec{spec}, IndexesCheck)
	if err != nil {
		t.Fatal(err)
	}
	if len(missing) != 0 {
		t.Fatalf("missing = %v, want none (the index already exists)", missing)
	}
}

// TestEnsureIndexesCompoundWithDescActuallyRuns executes the compound/
// DESC path for real: buildIndexSQL's unit tests only check the
// generated SQL's text, never run it. A single-expression index
// needed an extra, non-obvious pair of parens to even parse
// (TestBuildIndexSQLDefaultName's sibling found that the hard way, by
// running it) -- a compound index with a DESC column has its own
// chance to be subtly wrong the same way, so this runs it against
// real Postgres instead of trusting the single-field case generalizes.
func TestEnsureIndexesCompoundWithDescActuallyRuns(t *testing.T) {
	pool := testPool(t)
	table := "indexes_compound_desc_test"
	setupDocTable(t, pool, table)
	spec := IndexSpec{Table: table, Fields: []IndexField{
		{Field: "a"},
		{Field: "b", Desc: true},
	}}
	if _, err := EnsureIndexes(context.Background(), pool, []IndexSpec{spec}, IndexesCreate); err != nil {
		t.Fatal(err)
	}
	exists, err := indexExists(context.Background(), pool, table, "indexes_compound_desc_test_a_b_idx")
	if err != nil {
		t.Fatal(err)
	}
	if !exists {
		t.Fatal("compound index was not created")
	}
}

// TestEnsureIndexesExplain is the same lenient, logged-not-asserted
// check TestCompileFilterIntegrationExplainUsesIndex (query_integration_test.go)
// already uses for section 5's unique index: query.go's compiler
// parameterizes every field *name* too (docRef/dateTimeRef bind field
// as $N, never as a literal in the SQL text), so a literal-expression
// index built here is often not structurally matchable by the planner
// against a CompileFilter-built query, even when it indexes the exact
// same expression -- a known, already-accepted limitation (not
// something this test tries to fix), so this only documents what
// happens, never fails the build over it.
func TestEnsureIndexesExplain(t *testing.T) {
	pool := testPool(t)
	table := "indexes_explain_test"
	setupDocTable(t, pool, table)
	if err := EnsureFunctions(context.Background(), pool); err != nil {
		t.Fatal(err)
	}
	specs := []IndexSpec{
		{Table: table, Unique: true, Sparse: true, Fields: []IndexField{{Field: "slug"}}},
		{Table: table, Fields: []IndexField{{Field: "published_at", Kind: KindDateTime}}},
	}
	if _, err := EnsureIndexes(context.Background(), pool, specs, IndexesCreate); err != nil {
		t.Fatal(err)
	}

	const n = 3000
	docs := make([]string, n)
	for i := range docs {
		docs[i] = fmt.Sprintf(`{"name": "row-%d", "slug": "slug-%d", "published_at": "2020-01-01T00:00:00Z"}`, i, i)
	}
	insertDocs(t, pool, table, docs...)
	if err := pool.QueryRow(context.Background(), "ANALYZE "+table).Scan(); err != nil && err.Error() != "no rows in result set" {
		t.Fatal(err)
	}

	schema := SchemaSpec{Fields: map[string]Kind{"published_at": KindDateTime}}
	for _, q := range []Query{
		{"slug": "slug-1500"},
		{"published_at": Query{"$eq": "2020-01-01T00:00:00Z"}},
	} {
		where, args, err := CompileFilter(q, schema, 0)
		if err != nil {
			t.Fatal(err)
		}
		rows, err := pool.Query(context.Background(), "EXPLAIN (ANALYZE, BUFFERS) SELECT 1 FROM "+table+" WHERE "+where, args...)
		if err != nil {
			t.Fatal(err)
		}
		var plan strings.Builder
		for rows.Next() {
			var line string
			if err := rows.Scan(&line); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			plan.WriteString(line)
			plan.WriteString("\n")
		}
		rows.Close()
		t.Logf("EXPLAIN (ANALYZE, BUFFERS) for %v:\n%s", q, plan.String())
		if !strings.Contains(plan.String(), "Index") {
			t.Logf("note: planner did not pick an index-based plan for this run -- not asserted as a failure, same precedent as TestCompileFilterIntegrationExplainUsesIndex")
		}
	}
}
