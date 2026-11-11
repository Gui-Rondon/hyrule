package main

import (
	"net/http"
	"testing"

	"github.com/Gui-Rondon/hyrule/platform/config"
	"github.com/Gui-Rondon/hyrule/testkit"
)

func TestCRUDAndSlugLookup(t *testing.T) {
	c, _ := testkit.Service(t, spec(), config.Config{}, testkit.DisabledAuth(t))

	res := c.Do("POST", "/tags", map[string]any{"name": "Go", "slug": "go"})
	if res.Status != http.StatusCreated {
		t.Fatalf("POST = %d %s", res.Status, res.Raw)
	}

	res = c.Do("GET", "/tags/go", nil)
	if res.Status != http.StatusOK || res.String("name") != "Go" {
		t.Errorf("GET by slug = %d %s", res.Status, res.Raw)
	}

	res = c.Do("POST", "/tags", map[string]any{"name": "Golang", "slug": "go"})
	if res.Status != http.StatusUnprocessableEntity {
		t.Errorf("duplicate slug = %d %s", res.Status, res.Raw)
	}
}
