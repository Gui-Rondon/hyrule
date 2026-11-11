package postgres

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func testPool(t *testing.T) *pgxpool.Pool {
	uri := os.Getenv("POSTGRES_TEST_URI")
	if uri == "" {
		t.Skip("POSTGRES_TEST_URI not set")
	}
	pool, err := pgxpool.New(context.Background(), uri)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := pool.Ping(context.Background()); err != nil {
		t.Fatal(err)
	}
	return pool
}

func setupTable(t *testing.T, pool *pgxpool.Pool, table, ddl string) {
	ctx := context.Background()
	if _, err := pool.Exec(ctx, "DROP TABLE IF EXISTS "+table); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, ddl); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pool.Exec(context.Background(), "DROP TABLE IF EXISTS "+table) })
}

func TestStoreCRUD(t *testing.T) {
	pool := testPool(t)
	table := "store_crud_test"
	setupTable(t, pool, table, `CREATE TABLE `+table+` (
		id SERIAL PRIMARY KEY,
		name TEXT NOT NULL UNIQUE,
		status TEXT NOT NULL
	)`)
	ctx := context.Background()
	store := NewStore(pool, table)

	if err := store.InsertMany(ctx, []map[string]any{
		{"name": "a", "status": "draft"},
		{"name": "b", "status": "draft"},
		{"name": "c", "status": "published"},
	}); err != nil {
		t.Fatal(err)
	}

	n, err := store.CountDocuments(ctx, Filter{"status": "draft"})
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("count = %d, want 2", n)
	}

	docs, err := store.Find(ctx, Filter{"status": "draft"}, 0, 0, []SortField{{Column: "name"}}, []string{"name", "status"})
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 2 || docs[0]["name"] != "a" || docs[1]["name"] != "b" {
		t.Fatalf("docs = %#v", docs)
	}

	one, err := store.FindOne(ctx, Filter{"name": "c"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if one["status"] != "published" {
		t.Fatalf("one = %#v", one)
	}

	matched, err := store.UpdateOne(ctx, Filter{"name": "a"}, Filter{"status": "published"})
	if err != nil {
		t.Fatal(err)
	}
	if matched != 1 {
		t.Fatalf("matched = %d, want 1", matched)
	}
	after, err := store.FindOne(ctx, Filter{"name": "a"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if after["status"] != "published" {
		t.Fatalf("after update, status = %v", after["status"])
	}

	deleted, err := store.DeleteOne(ctx, Filter{"name": "b"})
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 1 {
		t.Fatalf("deleted = %d, want 1", deleted)
	}
	if _, err := store.FindOne(ctx, Filter{"name": "b"}, nil); err == nil {
		t.Fatal("expected FindOne to fail after delete")
	}
}

func TestIsDuplicateKeyError(t *testing.T) {
	pool := testPool(t)
	table := "store_dup_test"
	setupTable(t, pool, table, `CREATE TABLE `+table+` (name TEXT PRIMARY KEY)`)
	ctx := context.Background()
	store := NewStore(pool, table)

	if err := store.InsertMany(ctx, []map[string]any{{"name": "x"}}); err != nil {
		t.Fatal(err)
	}
	err := store.InsertMany(ctx, []map[string]any{{"name": "x"}})
	if err == nil {
		t.Fatal("expected an error inserting a duplicate key")
	}
	if !IsDuplicateKeyError(err) {
		t.Fatalf("IsDuplicateKeyError(%v) = false", err)
	}
}

func TestFindOrderingPaginationAndProjection(t *testing.T) {
	pool := testPool(t)
	table := "store_find_test"
	setupTable(t, pool, table, `CREATE TABLE `+table+` (
		id SERIAL PRIMARY KEY,
		name TEXT NOT NULL,
		rank INT NOT NULL
	)`)
	ctx := context.Background()
	store := NewStore(pool, table)
	if err := store.InsertMany(ctx, []map[string]any{
		{"name": "a", "rank": 3},
		{"name": "b", "rank": 1},
		{"name": "c", "rank": 2},
	}); err != nil {
		t.Fatal(err)
	}

	// Desc order.
	docs, err := store.Find(ctx, nil, 0, 0, []SortField{{Column: "rank", Desc: true}}, []string{"name"})
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 3 || docs[0]["name"] != "a" || docs[1]["name"] != "c" || docs[2]["name"] != "b" {
		t.Fatalf("docs = %#v", docs)
	}
	// Projection: only the requested column comes back.
	for _, d := range docs {
		if _, ok := d["rank"]; ok {
			t.Fatalf("projection leaked rank: %#v", d)
		}
	}

	// Pagination: skip 1, limit 1, ascending by rank -> the middle row ("c").
	page, err := store.Find(ctx, nil, 1, 1, []SortField{{Column: "rank"}}, []string{"name"})
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 1 || page[0]["name"] != "c" {
		t.Fatalf("page = %#v", page)
	}
}

func TestFindNoLimitIgnoresSkip(t *testing.T) {
	// limit <= 0 means no LIMIT/OFFSET is applied at all, even if skip is
	// set — matching mongodb.Store's documented behavior. Lock that in.
	pool := testPool(t)
	table := "store_find_nolimit_test"
	setupTable(t, pool, table, `CREATE TABLE `+table+` (name TEXT NOT NULL)`)
	ctx := context.Background()
	store := NewStore(pool, table)
	if err := store.InsertMany(ctx, []map[string]any{{"name": "a"}, {"name": "b"}}); err != nil {
		t.Fatal(err)
	}
	docs, err := store.Find(ctx, nil, 10, 0, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 2 {
		t.Fatalf("docs = %#v, want both rows despite skip=10 (limit<=0 disables it)", docs)
	}
}

func TestFindOneNotFound(t *testing.T) {
	pool := testPool(t)
	table := "store_findone_notfound_test"
	setupTable(t, pool, table, `CREATE TABLE `+table+` (name TEXT NOT NULL)`)
	ctx := context.Background()
	store := NewStore(pool, table)

	_, err := store.FindOne(ctx, Filter{"name": "missing"}, nil)
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("err = %v, want pgx.ErrNoRows", err)
	}
}

func TestQueryErrorsPropagate(t *testing.T) {
	// A filter/order/column referencing a column that doesn't exist must
	// surface as an error from every method, never silently return an
	// empty/zero result.
	pool := testPool(t)
	table := "store_query_error_test"
	setupTable(t, pool, table, `CREATE TABLE `+table+` (name TEXT NOT NULL)`)
	ctx := context.Background()
	store := NewStore(pool, table)

	if _, err := store.Find(ctx, Filter{"nope": 1}, 0, 0, nil, nil); err == nil {
		t.Error("Find: expected an error for a nonexistent column")
	}
	if _, err := store.CountDocuments(ctx, Filter{"nope": 1}); err == nil {
		t.Error("CountDocuments: expected an error for a nonexistent column")
	}
	if _, err := store.FindOne(ctx, Filter{"nope": 1}, nil); err == nil {
		t.Error("FindOne: expected an error for a nonexistent column")
	}
	if _, err := store.UpdateOne(ctx, Filter{"nope": 1}, Filter{"name": "x"}); err == nil {
		t.Error("UpdateOne: expected an error for a nonexistent filter column")
	}
	if _, err := store.DeleteOne(ctx, Filter{"nope": 1}); err == nil {
		t.Error("DeleteOne: expected an error for a nonexistent column")
	}
}

func TestFilterNilMeansIsNull(t *testing.T) {
	// Regression test: Filter{"col": nil} must behave as "col IS NULL",
	// not as the always-false "col = NULL".
	pool := testPool(t)
	table := "store_null_test"
	setupTable(t, pool, table, `CREATE TABLE `+table+` (name TEXT NOT NULL, category TEXT)`)
	ctx := context.Background()
	store := NewStore(pool, table)
	if err := store.InsertMany(ctx, []map[string]any{
		{"name": "a", "category": nil},
		{"name": "b", "category": "tech"},
	}); err != nil {
		t.Fatal(err)
	}

	n, err := store.CountDocuments(ctx, Filter{"category": nil})
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("count with category IS NULL = %d, want 1", n)
	}

	one, err := store.FindOne(ctx, Filter{"category": nil}, []string{"name"})
	if err != nil {
		t.Fatal(err)
	}
	if one["name"] != "a" {
		t.Fatalf("FindOne with nil filter = %#v", one)
	}

	matched, err := store.UpdateOne(ctx, Filter{"category": nil}, Filter{"category": "uncategorized"})
	if err != nil {
		t.Fatal(err)
	}
	if matched != 1 {
		t.Fatalf("matched = %d, want 1", matched)
	}

	deleted, err := store.DeleteOne(ctx, Filter{"category": nil})
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 0 {
		t.Fatalf("deleted = %d, want 0 (the only NULL row was already updated away)", deleted)
	}
}

func TestInsertManyEmpty(t *testing.T) {
	pool := testPool(t)
	table := "store_insert_empty_test"
	setupTable(t, pool, table, `CREATE TABLE `+table+` (name TEXT NOT NULL)`)
	ctx := context.Background()
	store := NewStore(pool, table)

	if err := store.InsertMany(ctx, nil); err != nil {
		t.Fatalf("InsertMany(nil) = %v, want nil", err)
	}
	if err := store.InsertMany(ctx, []map[string]any{}); err != nil {
		t.Fatalf("InsertMany([]) = %v, want nil", err)
	}
	n, err := store.CountDocuments(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("count = %d, want 0", n)
	}
}

func TestInsertManyRejectsMismatchedKeys(t *testing.T) {
	pool := testPool(t)
	table := "store_insert_mismatch_test"
	setupTable(t, pool, table, `CREATE TABLE `+table+` (name TEXT NOT NULL, status TEXT)`)
	ctx := context.Background()
	store := NewStore(pool, table)

	// docs[1] is missing "status", present in docs[0].
	err := store.InsertMany(ctx, []map[string]any{
		{"name": "a", "status": "draft"},
		{"name": "b"},
	})
	if err == nil {
		t.Fatal("expected an error for a doc missing a key present in docs[0]")
	}

	// docs[1] has an extra key, "extra", not present in docs[0].
	err = store.InsertMany(ctx, []map[string]any{
		{"name": "a", "status": "draft"},
		{"name": "b", "status": "draft", "extra": true},
	})
	if err == nil {
		t.Fatal("expected an error for a doc with an extra key not present in docs[0]")
	}

	// Same key *count* as docs[0], but a different key name — the length
	// check alone wouldn't catch this; the per-key presence check must.
	err = store.InsertMany(ctx, []map[string]any{
		{"name": "a", "status": "draft"},
		{"name": "b", "state": "draft"},
	})
	if err == nil {
		t.Fatal("expected an error for a doc with a same-count but different key than docs[0]")
	}

	// None of the three calls above should have inserted anything,
	// including docs[0]'s otherwise-valid row: validation runs before
	// any statement is sent.
	n, err := store.CountDocuments(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("count = %d, want 0 (rejected docs must not partially insert)", n)
	}
}

func TestUpdateOneEmptyUpdateIsNoop(t *testing.T) {
	pool := testPool(t)
	table := "store_update_noop_test"
	setupTable(t, pool, table, `CREATE TABLE `+table+` (name TEXT NOT NULL)`)
	ctx := context.Background()
	store := NewStore(pool, table)
	if err := store.InsertMany(ctx, []map[string]any{{"name": "a"}}); err != nil {
		t.Fatal(err)
	}

	matched, err := store.UpdateOne(ctx, Filter{"name": "a"}, Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if matched != 0 {
		t.Fatalf("matched = %d, want 0 (nothing to set)", matched)
	}
}

func TestUpdateOneAndDeleteOneNoMatch(t *testing.T) {
	pool := testPool(t)
	table := "store_nomatch_test"
	setupTable(t, pool, table, `CREATE TABLE `+table+` (name TEXT NOT NULL)`)
	ctx := context.Background()
	store := NewStore(pool, table)

	matched, err := store.UpdateOne(ctx, Filter{"name": "missing"}, Filter{"name": "x"})
	if err != nil {
		t.Fatal(err)
	}
	if matched != 0 {
		t.Fatalf("matched = %d, want 0", matched)
	}

	deleted, err := store.DeleteOne(ctx, Filter{"name": "missing"})
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 0 {
		t.Fatalf("deleted = %d, want 0", deleted)
	}
}

func TestReplaceOne(t *testing.T) {
	pool := testPool(t)
	table := "store_replace_test"
	setupTable(t, pool, table, `CREATE TABLE `+table+` (name TEXT NOT NULL, status TEXT NOT NULL)`)
	ctx := context.Background()
	store := NewStore(pool, table)
	if err := store.InsertMany(ctx, []map[string]any{{"name": "a", "status": "draft"}}); err != nil {
		t.Fatal(err)
	}

	matched, err := store.ReplaceOne(ctx, Filter{"name": "a"}, map[string]any{"status": "published"})
	if err != nil {
		t.Fatal(err)
	}
	if matched != 1 {
		t.Fatalf("matched = %d, want 1", matched)
	}
	after, err := store.FindOne(ctx, Filter{"name": "a"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if after["status"] != "published" {
		t.Fatalf("after = %#v", after)
	}
}
