package main

import (
	"net/http"
	"testing"

	"github.com/Gui-Rondon/hyrule/platform/config"
	"github.com/Gui-Rondon/hyrule/testkit"
)

func TestPathIsComputedFromParent(t *testing.T) {
	c, _ := testkit.Service(t, spec(), config.Config{}, testkit.DisabledAuth(t))

	res := c.Do("POST", "/categories", map[string]any{"name": "Tech", "slug": "tech"})
	if res.Status != http.StatusCreated {
		t.Fatalf("POST tech = %d %s", res.Status, res.Raw)
	}
	techID := res.String("_id")

	res = c.Do("POST", "/categories", map[string]any{"name": "Programming", "slug": "programming", "parent_id": techID})
	if res.Status != http.StatusCreated {
		t.Fatalf("POST programming = %d %s", res.Status, res.Raw)
	}
	// the write response only carries the meta fields; read it back to see
	// the computed "path"
	got := c.Do("GET", "/categories/programming", nil)
	if got.Status != http.StatusOK || got.String("path") != "tech/programming" {
		t.Errorf("GET programming = %d %q", got.Status, got.String("path"))
	}
}

func TestTreeEndpoint(t *testing.T) {
	c, _ := testkit.Service(t, spec(), config.Config{}, testkit.DisabledAuth(t))
	techID := c.Do("POST", "/categories", map[string]any{"name": "Tech", "slug": "tech"}).String("_id")
	c.Do("POST", "/categories", map[string]any{"name": "Programming", "slug": "programming", "parent_id": techID})

	res := c.Do("GET", "/categories/tree", nil)
	if res.Status != http.StatusOK {
		t.Fatalf("GET tree = %d %s", res.Status, res.Raw)
	}
	roots, _ := res.JSON.([]any)
	if len(roots) != 1 {
		t.Fatalf("roots = %v", roots)
	}
	root := roots[0].(map[string]any)
	items, _ := root["items"].([]any)
	if root["slug"] != "tech" || len(items) != 1 {
		t.Errorf("root = %v", root)
	}

	res = c.Do("GET", "/categories/tree/tech/programming", nil)
	if res.Status != http.StatusOK || res.String("slug") != "programming" {
		t.Errorf("GET tree by path = %d %s", res.Status, res.Raw)
	}
}
