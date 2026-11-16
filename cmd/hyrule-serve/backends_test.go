package main

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	_ "modernc.org/sqlite"

	"github.com/Gui-Rondon/hyrule/app"
	"github.com/Gui-Rondon/hyrule/rest"
	"github.com/Gui-Rondon/hyrule/testkit"
)

// Each test below calls a single Backend.Attach directly, against a real
// database, without parsing any YAML or serving HTTP -- the dispatch
// logic (declarative.File.Build, Resource.Backend) is covered on its own
// in declarative/backend_test.go, with fakes.

func TestMongoBackendAttach(t *testing.T) {
	db := testkit.Mongo(t)
	env := &app.Env{DB: db}
	res := &rest.Resource{Name: "items", Schema: rest.Schema{"name": {Type: rest.TypeString}}}
	if err := (mongoBackend{env}).Attach(context.Background(), res, "items"); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	if res.Store == nil {
		t.Fatal("Store not set")
	}
}

func TestMongoBackendAttach_WrongProcessBackend(t *testing.T) {
	env := &app.Env{} // DB nil: process was built for a different backend
	res := &rest.Resource{Name: "items"}
	err := (mongoBackend{env}).Attach(context.Background(), res, "items")
	if err == nil || !strings.Contains(err.Error(), "BACKEND=mongo") {
		t.Fatalf("Attach error = %v", err)
	}
}

func postgresTestPool(t *testing.T) *pgxpool.Pool {
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
	return pool
}

// postgresTestTable mirrors app/app_postgres_test.go's helper of the
// same name -- the jsonb-document table shape restpostgres expects.
func postgresTestTable(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	table := "hyrule_serve_test_" + strings.ToLower(strconv.FormatInt(time.Now().UnixNano(), 36))
	ddl := `CREATE TABLE ` + table + ` (
		id         uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
		doc        jsonb       NOT NULL DEFAULT '{}',
		created_at timestamptz NOT NULL DEFAULT now(),
		updated_at timestamptz NOT NULL DEFAULT now(),
		version    bigint      NOT NULL DEFAULT 1,
		deleted    boolean     NOT NULL DEFAULT false
	)`
	if _, err := pool.Exec(context.Background(), ddl); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pool.Exec(context.Background(), "DROP TABLE IF EXISTS "+table) })
	return table
}

func TestPostgresBackendAttach(t *testing.T) {
	pool := postgresTestPool(t)
	table := postgresTestTable(t, pool)
	env := &app.Env{Postgres: pool}
	res := &rest.Resource{Name: "items", Schema: rest.Schema{"name": {Type: rest.TypeString}}}
	if err := (postgresBackend{env}).Attach(context.Background(), res, table); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	if res.Adapter == nil {
		t.Fatal("Adapter not set")
	}
}

func TestPostgresBackendAttach_WrongProcessBackend(t *testing.T) {
	env := &app.Env{} // Postgres nil: process was built for a different backend
	res := &rest.Resource{Name: "items"}
	err := (postgresBackend{env}).Attach(context.Background(), res, "items")
	if err == nil || !strings.Contains(err.Error(), "BACKEND=postgres") {
		t.Fatalf("Attach error = %v", err)
	}
}

func sqliteTestDB(t *testing.T) *sql.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "hyrule-serve-test.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestSQLiteBackendAttach(t *testing.T) {
	db := sqliteTestDB(t)
	env := &app.Env{SQLite: db}
	res := &rest.Resource{Name: "items", Schema: rest.Schema{"name": {Type: rest.TypeString}}}
	if err := (sqliteBackend{env}).Attach(context.Background(), res, "items"); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	if res.Adapter == nil {
		t.Fatal("Adapter not set")
	}
}

func TestSQLiteBackendAttach_WrongProcessBackend(t *testing.T) {
	env := &app.Env{} // SQLite nil: process was built for a different backend
	res := &rest.Resource{Name: "items"}
	err := (sqliteBackend{env}).Attach(context.Background(), res, "items")
	if err == nil || !strings.Contains(err.Error(), "BACKEND=sqlite") {
		t.Fatalf("Attach error = %v", err)
	}
}
