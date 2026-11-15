package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4/database"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Gui-Rondon/hyrule/app"
	"github.com/Gui-Rondon/hyrule/examples/blog-apis/internal/commentsschema"
	"github.com/Gui-Rondon/hyrule/platform/config"
	"github.com/Gui-Rondon/hyrule/testkit"
)

func migrationPool(t *testing.T, uri string) *pgxpool.Pool {
	return gatePool(t, uri, "a5_", nil)
}

func gatePool(t *testing.T, uri, prefix string, tracer pgx.QueryTracer) *pgxpool.Pool {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	admin, err := pgxpool.New(ctx, uri)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(admin.Close)
	schema := prefix + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := admin.Exec(ctx, "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE"); err != nil {
			t.Error(err)
		}
	})
	cfg := admin.Config()
	cfg.MaxConns = 6
	cfg.ConnConfig.Tracer = tracer
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	cfg.ConnConfig.RuntimeParams["application_name"] = schema
	cfg.ConnConfig.RuntimeParams["statement_timeout"] = "5s"
	cfg.ConnConfig.RuntimeParams["lock_timeout"] = "2s"
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	t.Logf("isolated schema=%s", schema)
	return pool
}

func openMigration(t *testing.T, pool *pgxpool.Pool) *commentsschema.Migrator {
	t.Helper()
	m, err := commentsschema.Open(context.Background(), pool)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Close() })
	return m
}

func migratedHTTP(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	h, err := app.Build(context.Background(), spec(), app.Deps{Postgres: pool, Auth: testkit.DisabledAuth(t), Config: config.Config{"pubsub": map[string]any{"project_id": "local-dev", "healthcheck": false}}})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(h)
	t.Cleanup(server.Close)
	return server.URL
}

func migrationHTTP(t *testing.T, base, method, path string, body any, etag string) (int, map[string]any) {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, base+path, bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if etag != "" {
		req.Header.Set("If-Match", `"`+etag+`"`)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, err = io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("HTTP %d: %s", resp.StatusCode, raw)
	}
	return resp.StatusCode, doc
}

func createMigratedComment(t *testing.T, base, post string) map[string]any {
	t.Helper()
	code, doc := migrationHTTP(t, base, "POST", "/comments", map[string]any{"post_id": post, "author_name": "Link", "body": "migration proof"}, "")
	if code != http.StatusCreated {
		t.Fatalf("POST=%d %v", code, doc)
	}
	return doc
}

func TestMigrationLifecycle(t *testing.T) {
	if os.Getenv("POSTGRES_MIGRATIONS_TEST") != "1" {
		t.Skip("manual local gate: make postgres-ha-migrations-test")
	}
	uri := os.Getenv("POSTGRES_MIGRATIONS_URI")
	if uri == "" {
		t.Fatal("POSTGRES_MIGRATIONS_URI required")
	}
	t.Setenv("PUBSUB_EMULATOR_HOST", "localhost:8085")
	t.Run("expand-backfill-contract", func(t *testing.T) {
		pool := migrationPool(t, uri)
		ctx := context.Background()
		if err := commentsschema.Check(ctx, pool, "comments"); err == nil {
			t.Fatal("unmigrated schema accepted")
		}
		var created bool
		if err := pool.QueryRow(ctx, "SELECT to_regclass('comments') IS NOT NULL").Scan(&created); err != nil || created {
			t.Fatalf("startup check created table: %v", err)
		}
		m := openMigration(t, pool)
		defer m.Close()
		if err := commentsschema.To(m, 1); err != nil {
			t.Fatal(err)
		}
		base := migratedHTTP(t, pool)
		old := createMigratedComment(t, base, "old-post")
		for range 4 {
			createMigratedComment(t, base, "other-old-post")
		}
		if err := commentsschema.To(m, 2); err != nil {
			t.Fatal(err)
		}
		expanded := createMigratedComment(t, base, "expanded-post")
		code, patched := migrationHTTP(t, base, "PATCH", "/comments/"+expanded["_id"].(string), map[string]any{"post_id": "patched-post"}, expanded["_etag"].(string))
		if code != http.StatusOK {
			t.Fatalf("PATCH=%d %v", code, patched)
		}
		var post string
		if err := pool.QueryRow(ctx, "SELECT post_id FROM comments WHERE id=$1::uuid", expanded["_id"]).Scan(&post); err != nil || post != "patched-post" {
			t.Fatalf("trigger sync=%s err=%v", post, err)
		}
		n, err := commentsschema.Backfill(ctx, pool, 2)
		if err != nil || n != 5 {
			t.Fatalf("backfill rows=%d err=%v", n, err)
		}
		n, err = commentsschema.Backfill(ctx, pool, 2)
		if err != nil || n != 0 {
			t.Fatalf("resume rows=%d err=%v", n, err)
		}
		if err := commentsschema.To(m, 3); err != nil {
			t.Fatal(err)
		}
		if err := commentsschema.To(m, 3); err != nil {
			t.Fatal(err)
		}
		createMigratedComment(t, base, "contracted-post")
		code, read := migrationHTTP(t, base, "GET", "/comments/"+old["_id"].(string), nil, "")
		if code != http.StatusOK || read["_etag"] != old["_etag"] || read["body"] != "migration proof" {
			t.Fatalf("backfill changed document/etag: %d %v", code, read)
		}
		var missing, inconsistent int
		if err := pool.QueryRow(ctx, "SELECT count(*) FILTER (WHERE post_id IS NULL),count(*) FILTER (WHERE post_id IS DISTINCT FROM doc->>'post_id') FROM comments").Scan(&missing, &inconsistent); err != nil || missing != 0 || inconsistent != 0 {
			t.Fatalf("invalid projection: %d %d %v", missing, inconsistent, err)
		}
		if _, err := pool.Exec(ctx, "INSERT INTO comments(doc) VALUES ('{}')"); err == nil {
			t.Fatal("contract accepted missing post_id")
		}
		if err := commentsschema.To(m, 1); err == nil {
			t.Fatal("implicit downgrade accepted")
		}
		t.Log("same HTTP server: writes before/during/after; PATCH synchronized; five legacy rows backfilled in batches of two; etags preserved; clean version 3")
	})
	t.Run("no-search-path-fallback", func(t *testing.T) {
		migrated := migrationPool(t, uri)
		empty := migrationPool(t, uri)
		m := openMigration(t, migrated)
		if err := commentsschema.To(m, 1); err != nil {
			t.Fatal(err)
		}
		var first, second string
		if err := empty.QueryRow(context.Background(), "SELECT current_schema()").Scan(&first); err != nil {
			t.Fatal(err)
		}
		if err := migrated.QueryRow(context.Background(), "SELECT current_schema()").Scan(&second); err != nil {
			t.Fatal(err)
		}
		cfg := empty.Config()
		cfg.ConnConfig.RuntimeParams["search_path"] = first + "," + second
		fallback, err := pgxpool.NewWithConfig(context.Background(), cfg)
		if err != nil {
			t.Fatal(err)
		}
		defer fallback.Close()
		if err := commentsschema.Check(context.Background(), fallback, "comments"); err == nil {
			t.Fatal("startup accepted another schema's migration state")
		}
	})
	t.Run("adopt-legacy", func(t *testing.T) {
		pool := migrationPool(t, uri)
		m := openMigration(t, pool)
		if err := commentsschema.To(m, 1); err != nil {
			t.Fatal(err)
		}
		m.Close()
		if _, err := pool.Exec(context.Background(), `INSERT INTO comments(doc) VALUES ('{"post_id":"legacy","body":"keep"}'); DROP TABLE comments_schema_migrations`); err != nil {
			t.Fatal(err)
		}
		m = openMigration(t, pool)
		defer m.Close()
		if err := commentsschema.To(m, 1); err != nil {
			t.Fatal(err)
		}
		var body string
		if err := pool.QueryRow(context.Background(), "SELECT doc->>'body' FROM comments").Scan(&body); err != nil || body != "keep" {
			t.Fatalf("adoption lost data: %s %v", body, err)
		}
	})
	t.Run("reject-incompatible-adoption", func(t *testing.T) {
		pool := migrationPool(t, uri)
		if _, err := pool.Exec(context.Background(), "CREATE TABLE comments (id text)"); err != nil {
			t.Fatal(err)
		}
		m := openMigration(t, pool)
		if err := commentsschema.To(m, 1); err == nil {
			t.Fatal("adopted wrong table")
		}
		m.Close()
		var typ string
		if err := pool.QueryRow(context.Background(), "SELECT atttypid::regtype::text FROM pg_attribute WHERE attrelid='comments'::regclass AND attname='id'").Scan(&typ); err != nil || typ != "text" {
			t.Fatalf("invalid table was altered: %s %v", typ, err)
		}
	})
	t.Run("dirty-state-explicit-repair", func(t *testing.T) {
		pool := migrationPool(t, uri)
		m := openMigration(t, pool)
		if err := commentsschema.To(m, 1); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(context.Background(), `INSERT INTO comments(doc) VALUES ('{"post_id":"legacy"}')`); err != nil {
			t.Fatal(err)
		}
		if err := commentsschema.To(m, 2); err != nil {
			t.Fatal(err)
		}
		if err := commentsschema.To(m, 3); err == nil {
			t.Fatal("contract without backfill succeeded")
		}
		m.Close() // failed SQL transaction must release/rollback before inspection
		m = openMigration(t, pool)
		defer m.Close()
		v, dirty, err := m.Version()
		if err != nil || v != 3 || !dirty {
			t.Fatalf("version=%d dirty=%v err=%v", v, dirty, err)
		}
		if err := commentsschema.Check(context.Background(), pool, "comments"); err == nil {
			t.Fatal("dirty startup accepted")
		}
		if err := commentsschema.To(m, 3); err == nil {
			t.Fatal("dirty state auto-repaired")
		}
		var notnull bool
		if err := pool.QueryRow(context.Background(), "SELECT attnotnull FROM pg_attribute WHERE attrelid='comments'::regclass AND attname='post_id'").Scan(&notnull); err != nil || notnull {
			t.Fatalf("failed contract not rolled back: %v", err)
		}
		// Operator-equivalent action AFTER proving migration 3 rolled back atomically.
		if err := m.Force(2); err != nil {
			t.Fatal(err)
		}
		if _, err := commentsschema.Backfill(context.Background(), pool, 1); err != nil {
			t.Fatal(err)
		}
		if err := commentsschema.To(m, 3); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("real-migrator-lock", func(t *testing.T) {
		pool := migrationPool(t, uri)
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		m := openMigration(t, pool)
		if err := commentsschema.To(m, 1); err != nil {
			t.Fatal(err)
		}
		m.Close()
		conn, err := pool.Acquire(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Release()
		var db, schema string
		if err := conn.QueryRow(ctx, "SELECT current_database(),current_schema()").Scan(&db, &schema); err != nil {
			t.Fatal(err)
		}
		key, err := database.GenerateAdvisoryLockId(db, schema, commentsschema.VersionTable)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", key); err != nil {
			t.Fatal(err)
		}
		defer conn.Exec(context.Background(), "SELECT pg_advisory_unlock($1)", key)
		done := make(chan error, 1)
		go func() {
			m, err := commentsschema.Open(ctx, pool)
			if err == nil {
				err = commentsschema.To(m, 2)
				m.Close()
			}
			done <- err
		}()
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for {
			var waiting bool
			if err := pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE application_name=$1 AND wait_event='advisory')", schema).Scan(&waiting); err != nil {
				t.Fatal(err)
			}
			if waiting {
				break
			}
			select {
			case err := <-done:
				t.Fatalf("migrator did not block: %v", err)
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			case <-ticker.C:
			}
		}
		if _, err := conn.Exec(ctx, "SELECT pg_advisory_unlock($1)", key); err != nil {
			t.Fatal(err)
		}
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
		t.Log("contending migrator observed waiting for PostgreSQL advisory lock; advanced only after release")
	})
	fmt.Println("A5 manual local migration lifecycle verified")
}
