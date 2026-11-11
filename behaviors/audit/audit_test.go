package audit_test

import (
	"net/http"
	"testing"

	"github.com/Gui-Rondon/hyrule/behaviors/audit"
	"github.com/Gui-Rondon/hyrule/platform/auth"
	"github.com/Gui-Rondon/hyrule/rest"
	"github.com/Gui-Rondon/hyrule/testkit"
)

func newAPI(t *testing.T, authn *auth.Authenticator) *testkit.Client {
	t.Helper()
	res := &rest.Resource{
		Name:  "items",
		Store: testkit.Store(t, "items"),
		// the APIs declare the audit fields: without allow_unknown, fields
		// outside the schema are not returned
		Schema: rest.Schema{"name": {Type: rest.TypeString},
			"_created_user": {Type: rest.TypeString}, "_updated_user": {Type: rest.TypeString}},
		ResourceMethods: []string{http.MethodGet, http.MethodPost},
		ItemMethods:     []string{http.MethodGet, http.MethodPatch, http.MethodPut},
	}
	res.Hooks.Use(audit.Hooks())
	api := rest.New(rest.Options{Auth: authn})
	api.Register(res)
	return testkit.NewClient(t, api)
}

// _created_user on insert, _updated_user on every write.
func TestAuditUsesTheTokenUser(t *testing.T) {
	keys := testkit.NewKeys(t)
	c := newAPI(t, keys.Authenticator(t))
	as := func(user string) []string { return []string{"Authorization", "Token " + keys.Token(t, user)} }

	created := c.Do("POST", "/items", map[string]any{"name": "a"}, as("fulano")...)
	id := created.String("_id")
	doc := c.Do("GET", "/items/"+id, nil, as("fulano")...).Map()
	if doc["_created_user"] != "fulano" || doc["_updated_user"] != "fulano" {
		t.Fatalf("after insert = %v", doc)
	}

	patched := c.Do("PATCH", "/items/"+id, map[string]any{"name": "b"}, append(as("beltrano"), "If-Match", created.String("_etag"))...)
	doc = c.Do("GET", "/items/"+id, nil, as("x")...).Map()
	if doc["_created_user"] != "fulano" || doc["_updated_user"] != "beltrano" {
		t.Fatalf("after patch = %v", doc)
	}

	// V-08: a PUT keeps the author
	c.Do("PUT", "/items/"+id, map[string]any{"name": "c"}, append(as("ciclano"), "If-Match", patched.String("_etag"))...)
	doc = c.Do("GET", "/items/"+id, nil, as("x")...).Map()
	if doc["_created_user"] != "fulano" || doc["_updated_user"] != "ciclano" {
		t.Errorf("after put = %v", doc)
	}
}

func TestAuditWithoutAuthenticationStoresNull(t *testing.T) {
	c := newAPI(t, nil)
	created := c.Do("POST", "/items", map[string]any{"name": "a"})
	doc := c.Do("GET", "/items/"+created.String("_id"), nil).Map()
	if v, ok := doc["_created_user"]; !ok || v != nil {
		t.Errorf("_created_user = %v (present %v)", v, ok)
	}
}
