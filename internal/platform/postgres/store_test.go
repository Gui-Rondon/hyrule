package postgres

import (
	"context"
	"os"
	"testing"

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
