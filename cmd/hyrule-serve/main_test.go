package main

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/Gui-Rondon/hyrule/internal/app"
	"github.com/Gui-Rondon/hyrule/internal/platform/config"
	"github.com/Gui-Rondon/hyrule/internal/testkit"
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

func TestRequiresResourcesFile(t *testing.T) {
	db := testkit.Mongo(t)
	_, err := app.Build(context.Background(), spec(), app.Deps{
		Config: config.Config{}, Mongo: db.Client(), DB: db, Auth: testkit.DisabledAuth(t),
	})
	if err == nil {
		t.Error("missing resources_file accepted")
	}
}
