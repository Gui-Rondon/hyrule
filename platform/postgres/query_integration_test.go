package postgres

import (
	"context"
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
