package main

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/Gui-Rondon/hyrule/app"
	"github.com/Gui-Rondon/hyrule/platform/config"
	"github.com/Gui-Rondon/hyrule/testkit"
)

const resourcesYAML = `
resources:
  - name: tags
    collection: tags
    additional_lookup:
      field: slug
      pattern: '^[\w-]+$'
    resource_methods: [get, post]
    item_methods: [get, patch]
    schema:
      name:
        type: string
        required: true
      slug:
        type: string
        required: true
        unique: true
`

func TestServeFromYAML(t *testing.T) {
	path := filepath.Join(t.TempDir(), "resources.yaml")
	if err := os.WriteFile(path, []byte(resourcesYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	c, _ := testkit.Service(t, spec(), config.Config{"resources_file": path}, testkit.DisabledAuth(t))

	created := c.Do("POST", "/tags", map[string]any{"name": "Go", "slug": "go"})
	if created.Status != http.StatusCreated {
		t.Fatalf("POST = %d %s", created.Status, created.Raw)
	}
	id, etag := created.String("_id"), created.String("_etag")

	if res := c.Do("GET", "/tags/go", nil); res.Status != http.StatusOK || res.String("name") != "Go" {
		t.Errorf("GET by slug = %d %s", res.Status, res.Raw)
	}

	if res := c.Do("PATCH", "/tags/"+id, map[string]any{"name": "Golang"}, "If-Match", etag); res.Status != http.StatusOK {
		t.Errorf("PATCH = %d %s", res.Status, res.Raw)
	}

	if res := c.Do("POST", "/tags", map[string]any{"name": "Golang", "slug": "go"}); res.Status != http.StatusUnprocessableEntity {
		t.Errorf("duplicate slug = %d %s", res.Status, res.Raw)
	}
}

// TestServeFromYAML_Postgres is TestServeFromYAML's counterpart for
// backend: postgres -- same resources.yaml shape, a real Postgres table
// instead of a Mongo collection, proving the YAML path is no longer
// Mongo-only end to end over HTTP.
func TestServeFromYAML_Postgres(t *testing.T) {
	pool := postgresTestPool(t)
	table := postgresTestTable(t, pool)
	yaml := `
resources:
  - name: items
    collection: ` + table + `
    backend: postgres
    resource_methods: [get, post]
    item_methods: [get, patch]
    schema:
      name:
        type: string
        required: true
`
	path := filepath.Join(t.TempDir(), "resources.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	handler, err := app.Build(context.Background(), app.Spec{Name: "hyrule-serve", Backend: app.BackendPostgres, Setup: setup}, app.Deps{
		Config: config.Config{"resources_file": path}, Postgres: pool, Auth: testkit.DisabledAuth(t),
	})
	if err != nil {
		t.Fatal(err)
	}
	c := testkit.NewClient(t, handler)

	created := c.Do("POST", "/items", map[string]any{"name": "a"})
	if created.Status != http.StatusCreated {
		t.Fatalf("POST = %d %s", created.Status, created.Raw)
	}
	id, etag := created.String("_id"), created.String("_etag")

	if res := c.Do("GET", "/items/"+id, nil); res.Status != http.StatusOK || res.String("name") != "a" {
		t.Errorf("GET by id = %d %s", res.Status, res.Raw)
	}
	if res := c.Do("PATCH", "/items/"+id, map[string]any{"name": "b"}, "If-Match", etag); res.Status != http.StatusOK {
		t.Errorf("PATCH = %d %s", res.Status, res.Raw)
	}
}

// TestServeFromYAML_SQLite is TestServeFromYAML's counterpart for
// backend: sqlite.
func TestServeFromYAML_SQLite(t *testing.T) {
	db := sqliteTestDB(t)
	const yaml = `
resources:
  - name: items
    collection: items
    backend: sqlite
    resource_methods: [get, post]
    item_methods: [get, patch]
    schema:
      name:
        type: string
        required: true
`
	path := filepath.Join(t.TempDir(), "resources.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	handler, err := app.Build(context.Background(), app.Spec{Name: "hyrule-serve", Backend: app.BackendSQLite, Setup: setup}, app.Deps{
		Config: config.Config{"resources_file": path}, SQLite: db, Auth: testkit.DisabledAuth(t),
	})
	if err != nil {
		t.Fatal(err)
	}
	c := testkit.NewClient(t, handler)

	created := c.Do("POST", "/items", map[string]any{"name": "a"})
	if created.Status != http.StatusCreated {
		t.Fatalf("POST = %d %s", created.Status, created.Raw)
	}
	id, etag := created.String("_id"), created.String("_etag")

	if res := c.Do("GET", "/items/"+id, nil); res.Status != http.StatusOK || res.String("name") != "a" {
		t.Errorf("GET by id = %d %s", res.Status, res.Raw)
	}
	if res := c.Do("PATCH", "/items/"+id, map[string]any{"name": "b"}, "If-Match", etag); res.Status != http.StatusOK {
		t.Errorf("PATCH = %d %s", res.Status, res.Raw)
	}
}

func TestRequiresResourcesFile(t *testing.T) {
	db := testkit.Mongo(t)
	_, err := app.Build(context.Background(), spec(), app.Deps{
		Config: config.Config{}, Mongo: db.Client(), DB: db, Auth: testkit.DisabledAuth(t),
	})
	if err == nil {
		t.Error("missing resources_file accepted")
	}
}
