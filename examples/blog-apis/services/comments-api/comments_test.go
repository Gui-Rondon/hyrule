package main

import (
	"context"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Gui-Rondon/hyrule/app"
	"github.com/Gui-Rondon/hyrule/examples/blog-apis/internal/commentsschema"
	"github.com/Gui-Rondon/hyrule/platform/config"
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

	schema := "comments_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := pool.Exec(context.Background(), "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(), "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE"); err != nil {
			t.Error(err)
		}
	})
	cfg := pool.Config()
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	isolated, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(isolated.Close)
	m, err := commentsschema.Open(context.Background(), isolated)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	if err := commentsschema.To(m, 1); err != nil {
		t.Fatal(err)
	}
	if err := commentsschema.To(m, 2); err != nil {
		t.Fatal(err)
	}
	if _, err := commentsschema.Backfill(context.Background(), isolated, 100); err != nil {
		t.Fatal(err)
	}
	if err := commentsschema.To(m, 3); err != nil {
		t.Fatal(err)
	}
	return isolated
}

// newTestClient builds the service the same way app.Main does
// (app.Build, app.BackendPostgres), against a fully migrated private schema.
func newTestClient(t *testing.T) *testkit.Client {
	t.Helper()
	pool := testPool(t)
	if os.Getenv("PUBSUB_EMULATOR_HOST") == "" {
		t.Setenv("PUBSUB_EMULATOR_HOST", "localhost:8085")
	}
	ctx := context.Background()
	table, outboxTable := "comments", "outbox"

	spec := app.Spec{
		Name:            "comments-api",
		Backend:         app.BackendPostgres,
		DefaultDB:       "comments",
		Version:         "test",
		Setup:           setupWithTables(table, outboxTable),
		PostgresIndexes: commentsIndexes(table),
	}
	handler, err := app.Build(ctx, spec, app.Deps{
		Config: config.Config{"pubsub": map[string]any{"project_id": "local-dev", "healthcheck": false}}, Postgres: pool, Auth: testkit.DisabledAuth(t),
	})
	if err != nil {
		t.Fatal(err)
	}
	return testkit.NewClient(t, handler)
}

// TestCRUDAndFilter drives the real HTTP surface app.Main builds end to
// end: create, read, list filtered by post_id (Fase 6's commentsIndexes
// is the index that query makes use of), update under If-Match, stale
// If-Match rejected, delete, then confirm it's gone -- the same shape
// examples/blog-apis/services/tags-api/tags_test.go exercises for the
// Mongo path, now against a real Postgres table.
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

// TestStatusAndHealthcheck proves comments-api now gets /status and
// /healthcheck for free from app.Main (finishBuild) -- comments-api
// never had either before B3, and the e2e harness's readiness probe
// moves onto /status because of this.
func TestStatusAndHealthcheck(t *testing.T) {
	c := newTestClient(t)

	status := c.Do("GET", "/status", nil)
	if status.Status != http.StatusOK || status.String("version") != "test" {
		t.Fatalf("GET /status = %d %v", status.Status, status.Map())
	}
	hc := c.Do("GET", "/healthcheck", nil)
	if hc.Status != http.StatusOK {
		t.Fatalf("GET /healthcheck = %d %s", hc.Status, hc.Raw)
	}
}
