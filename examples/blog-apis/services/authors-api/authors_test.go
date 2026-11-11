package main

import (
	"net/http"
	"testing"

	"github.com/Gui-Rondon/hyrule/platform/config"
	"github.com/Gui-Rondon/hyrule/testkit"
)

func TestCRUD(t *testing.T) {
	c, _ := testkit.Service(t, spec(), config.Config{}, testkit.DisabledAuth(t))

	res := c.Do("POST", "/authors", map[string]any{"name": "Ada Lovelace", "email": "ada@example.com"})
	if res.Status != http.StatusCreated {
		t.Fatalf("POST = %d %s", res.Status, res.Raw)
	}
	id := res.String("_id")

	res = c.Do("GET", "/authors/"+id, nil)
	if res.Status != http.StatusOK || res.String("name") != "Ada Lovelace" {
		t.Errorf("GET = %d %s", res.Status, res.Raw)
	}

	// duplicate email: a second author with the same email fails validation
	res = c.Do("POST", "/authors", map[string]any{"name": "Someone Else", "email": "ada@example.com"})
	if res.Status != http.StatusUnprocessableEntity {
		t.Errorf("duplicate email = %d %s", res.Status, res.Raw)
	}
}
