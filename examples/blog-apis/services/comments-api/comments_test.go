package main

import (
	"context"
	"net/http"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Gui-Rondon/hyrule/testkit"
)

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
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

func newTestClient(t *testing.T) *testkit.Client {
	t.Helper()
	pool := testPool(t)
	ctx := context.Background()
	table := "comments_test"
	if _, err := pool.Exec(ctx, "DROP TABLE IF EXISTS "+table); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pool.Exec(context.Background(), "DROP TABLE IF EXISTS "+table) })
	api, err := setup(ctx, pool, table)
	if err != nil {
		t.Fatal(err)
	}
	return testkit.NewClient(t, api)
}

// TestCRUDAndFilter drives the real HTTP surface setup() builds end
// to end: create, read, list filtered by post_id (Fase 6's
// commentsIndexes is the index that query makes use of), update under
// If-Match, stale If-Match rejected, delete, then confirm it's gone --
// the same shape examples/blog-apis/services/tags-api/tags_test.go
// exercises for the Mongo path, now against a real Postgres table.
func TestCRUDAndFilter(t *testing.T) {
	c := newTestClient(t)

	created := c.Do("POST", "/comments", map[string]any{
		"post_id":     "post-1",
		"author_name": "Link",
		"body":        "Nice post!",
	})
	if created.Status != http.StatusCreated {
		t.Fatalf("POST = %d %s", created.Status, created.Raw)
	}
	id, etag := created.String("_id"), created.String("_etag")
	if id == "" || etag == "" {
		t.Fatalf("POST body = %v", created.Map())
	}

	other := c.Do("POST", "/comments", map[string]any{
		"post_id":     "post-2",
		"author_name": "Zelda",
		"body":        "Unrelated post",
	})
	if other.Status != http.StatusCreated {
		t.Fatalf("POST (other post) = %d %s", other.Status, other.Raw)
	}

	got := c.Do("GET", "/comments/"+id, nil)
	if got.Status != http.StatusOK || got.String("body") != "Nice post!" {
		t.Fatalf("GET item = %d %v", got.Status, got.Map())
	}
	if got.String("status") != "pending" {
		t.Errorf("default status = %q, want pending", got.String("status"))
	}

	filtered := c.Do("GET", `/comments?where={"post_id":{"$eq":"post-1"}}`, nil)
	items, _ := filtered.Map()["_items"].([]any)
	if filtered.Status != http.StatusOK || len(items) != 1 {
		t.Fatalf("GET list where post_id=post-1 = %d %v", filtered.Status, filtered.Map())
	}

	wrongEtag := c.Do("PATCH", "/comments/"+id, map[string]any{"status": "published"}, "If-Match", `"bogus"`)
	if wrongEtag.Status != http.StatusPreconditionFailed {
		t.Fatalf("PATCH with wrong etag = %d %s", wrongEtag.Status, wrongEtag.Raw)
	}

	patched := c.Do("PATCH", "/comments/"+id, map[string]any{"status": "published"}, "If-Match", `"`+etag+`"`)
	if patched.Status != http.StatusOK {
		t.Fatalf("PATCH = %d %s", patched.Status, patched.Raw)
	}
	newEtag := patched.String("_etag")

	afterPatch := c.Do("GET", "/comments/"+id, nil)
	if afterPatch.String("status") != "published" {
		t.Fatalf("GET after PATCH status = %q, want published", afterPatch.String("status"))
	}

	deleted := c.Do("DELETE", "/comments/"+id, nil, "If-Match", `"`+newEtag+`"`)
	if deleted.Status != http.StatusNoContent {
		t.Fatalf("DELETE = %d %s", deleted.Status, deleted.Raw)
	}

	gone := c.Do("GET", "/comments/"+id, nil)
	if gone.Status != http.StatusNotFound {
		t.Fatalf("GET after DELETE = %d %v, want 404", gone.Status, gone.Map())
	}
}
