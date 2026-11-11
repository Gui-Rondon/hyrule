package main

import (
	"net/http"
	"testing"

	"github.com/Gui-Rondon/hyrule/platform/config"
	"github.com/Gui-Rondon/hyrule/testkit"
)

// testkit gives this test its own throwaway Mongo database, the same way
// it would inside the Hyrule module itself — nothing about testkit changes
// for an external consumer.
func TestTagsCRUD(t *testing.T) {
	c, _ := testkit.Service(t, spec(), config.Config{}, testkit.DisabledAuth(t))

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
