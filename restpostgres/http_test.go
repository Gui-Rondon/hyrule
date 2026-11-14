package restpostgres

import (
	"net/http"
	"testing"

	"github.com/Gui-Rondon/hyrule/platform/postgres"
	"github.com/Gui-Rondon/hyrule/rest"
	"github.com/Gui-Rondon/hyrule/testkit"
)

// newHTTPTestAPI wires a real postgresAdapter into a real rest.API and
// drives it over HTTP (testkit.NewClient) -- Fase 4b's actual deliverable:
// before it, no HTTP request could reach a Postgres resource at all
// (ParseListQuery/ListQuery stayed bson.M/bson.D-typed, and itemLookup/
// checkMatched hardcoded a Mongo bson.M{"_id": id} filter that
// postgresAdapter's CombineFilter/compileFilter couldn't compile).
func newHTTPTestAPI(t *testing.T, table string, softDelete bool, configure func(*rest.Resource)) *testkit.Client {
	t.Helper()
	pool := testPool(t)
	setupResourceTable(t, pool, table)
	schema := rest.Schema{
		"name":   {Type: rest.TypeString, Required: true},
		"status": {Type: rest.TypeString},
	}
	// SoftDelete is set on both Options (the adapter -- hydrate's
	// _deleted field, UpdateOne's deleted column write) and the
	// rest.Resource below (Service's soft-delete write/read path):
	// the two are independently set booleans nothing enforces agree
	// (query.go's CombineFilter doc comment), so a test building a
	// soft-delete resource must set both or get a resource that looks
	// soft-delete at the HTTP layer but never actually persists it.
	tx := postgres.Transactor{Pool: pool}
	adapter, err := NewAdapter(postgres.NewStore(pool, table), tx, Options{
		Schema:     schema,
		SoftDelete: softDelete,
		Retry:      rest.DefaultRetryOptions(),
	})
	if err != nil {
		t.Fatal(err)
	}
	res := &rest.Resource{
		Name:            table,
		Schema:          schema,
		Adapter:         adapter,
		SoftDelete:      softDelete,
		ResourceMethods: []string{http.MethodGet, http.MethodPost},
		ItemMethods:     []string{http.MethodGet, http.MethodPatch, http.MethodPut, http.MethodDelete},
	}
	if configure != nil {
		configure(res)
	}
	// The same Transactor given to NewAdapter above also drives
	// rest.Service's writes (Options.Transactor) -- so a resource's
	// hooks and its InsertMany/UpdateOne/DeleteOne share one real
	// Postgres transaction, the same way mongodb.TransactorFor wires a
	// Mongo resource's Service in rest/service_test.go's newAPI.
	api := rest.New(rest.Options{Transactor: tx})
	api.Register(res)
	return testkit.NewClient(t, api)
}

func TestHTTPCRUDEndToEnd(t *testing.T) {
	c := newHTTPTestAPI(t, "http_crud_test", false, nil)

	created := c.Do("POST", "/http_crud_test", map[string]any{"name": "a"})
	if created.Status != http.StatusCreated {
		t.Fatalf("POST = %d %s", created.Status, created.Raw)
	}
	id, etag := created.String("_id"), created.String("_etag")
	if id == "" || etag != "1" {
		t.Fatalf("POST body = %v", created.Map())
	}

	got := c.Do("GET", "/http_crud_test/"+id, nil)
	if got.Status != http.StatusOK || got.Map()["name"] != "a" {
		t.Fatalf("GET item = %d %v", got.Status, got.Map())
	}
	// Last-Modified comes from doc["_updated"], a plain time.Time on
	// Postgres (restpostgres/doc.go's hydrate) -- confirms http.go's
	// Last-Modified/If-Modified-Since logic no longer assumes
	// Mongo's bson.DateTime (rest/render.go's asTime).
	if got.Header.Get("Last-Modified") == "" {
		t.Error("GET item: Last-Modified header missing")
	}

	list := c.Do("GET", "/http_crud_test", nil)
	items, _ := list.Map()["_items"].([]any)
	if list.Status != http.StatusOK || len(items) != 1 {
		t.Fatalf("GET list = %d %v", list.Status, list.Map())
	}
	if list.Header.Get("Last-Modified") == "" {
		t.Error("GET list: Last-Modified header missing")
	}

	filtered := c.Do("GET", `/http_crud_test?where={"name":{"$eq":"a"}}`, nil)
	filteredItems, _ := filtered.Map()["_items"].([]any)
	if filtered.Status != http.StatusOK || len(filteredItems) != 1 {
		t.Fatalf("GET list with where = %d %v", filtered.Status, filtered.Map())
	}
	none := c.Do("GET", `/http_crud_test?where={"name":{"$eq":"nope"}}`, nil)
	noneItems, _ := none.Map()["_items"].([]any)
	if none.Status != http.StatusOK || len(noneItems) != 0 {
		t.Fatalf("GET list with non-matching where = %d %v", none.Status, none.Map())
	}

	wrongEtag := c.Do("PATCH", "/http_crud_test/"+id, map[string]any{"status": "done"}, "If-Match", "bogus")
	if wrongEtag.Status != http.StatusPreconditionFailed {
		t.Fatalf("PATCH with wrong etag = %d %s", wrongEtag.Status, wrongEtag.Raw)
	}

	// PATCH/PUT answer the BANDWIDTH_SAVER envelope (meta fields only,
	// rest.Service.WriteResult) -- the written document itself is
	// confirmed below with a plain GET, same as the Mongo-backed tests
	// (rest/service_test.go) already do.
	patched := c.Do("PATCH", "/http_crud_test/"+id, map[string]any{"status": "done"}, "If-Match", etag)
	if patched.Status != http.StatusOK || patched.Map()["_status"] != "OK" {
		t.Fatalf("PATCH = %d %v", patched.Status, patched.Map())
	}
	etag = patched.String("_etag")
	if etag != "2" {
		t.Errorf("etag after PATCH = %q, want \"2\"", etag)
	}
	afterPatch := c.Do("GET", "/http_crud_test/"+id, nil)
	if afterPatch.Map()["status"] != "done" || afterPatch.Map()["name"] != "a" {
		t.Fatalf("GET after PATCH = %v, want status=done, name=a still merged in", afterPatch.Map())
	}

	replaced := c.Do("PUT", "/http_crud_test/"+id, map[string]any{"name": "b"}, "If-Match", etag)
	if replaced.Status != http.StatusOK || replaced.Map()["_status"] != "OK" {
		t.Fatalf("PUT = %d %v", replaced.Status, replaced.Map())
	}
	etag = replaced.String("_etag")
	afterPut := c.Do("GET", "/http_crud_test/"+id, nil)
	if afterPut.Map()["name"] != "b" || afterPut.Map()["status"] != nil {
		t.Fatalf("GET after PUT = %v, want name=b and status gone (full replace)", afterPut.Map())
	}

	deleted := c.Do("DELETE", "/http_crud_test/"+id, nil, "If-Match", etag)
	if deleted.Status != http.StatusNoContent {
		t.Fatalf("DELETE = %d %s", deleted.Status, deleted.Raw)
	}

	gone := c.Do("GET", "/http_crud_test/"+id, nil)
	if gone.Status != http.StatusNotFound {
		t.Fatalf("GET after DELETE = %d", gone.Status)
	}
}

func TestHTTPSoftDeleteEndToEnd(t *testing.T) {
	c := newHTTPTestAPI(t, "http_softdelete_test", true, nil)

	created := c.Do("POST", "/http_softdelete_test", map[string]any{"name": "a"})
	if created.Status != http.StatusCreated || created.Map()["_deleted"] != false {
		t.Fatalf("POST = %d %v", created.Status, created.Map())
	}
	id, etag := created.String("_id"), created.String("_etag")

	deleted := c.Do("DELETE", "/http_softdelete_test/"+id, nil, "If-Match", etag)
	if deleted.Status != http.StatusNoContent {
		t.Fatalf("DELETE = %d %s", deleted.Status, deleted.Raw)
	}

	// A soft deleted item answers 404 on a plain GET...
	gone := c.Do("GET", "/http_softdelete_test/"+id, nil)
	if gone.Status != http.StatusNotFound {
		t.Fatalf("GET after soft delete = %d %s", gone.Status, gone.Raw)
	}
	// ...and is excluded from the collection by default...
	list := c.Do("GET", "/http_softdelete_test", nil)
	items, _ := list.Map()["_items"].([]any)
	if len(items) != 0 {
		t.Fatalf("GET list after soft delete = %v, want no items", items)
	}
	// ...but still reachable with show_deleted.
	shown := c.Do("GET", "/http_softdelete_test?show_deleted", nil)
	shownItems, _ := shown.Map()["_items"].([]any)
	if len(shownItems) != 1 {
		t.Fatalf("GET list with show_deleted = %v, want 1 item", shownItems)
	}
}

func TestHTTPRequireMergePatchContentType(t *testing.T) {
	c := newHTTPTestAPI(t, "http_mergepatch_ct_test", false, func(res *rest.Resource) {
		res.RequireMergePatchContentType = true
	})
	created := c.Do("POST", "/http_mergepatch_ct_test", map[string]any{"name": "a"})
	id, etag := created.String("_id"), created.String("_etag")

	wrongCT := c.Do("PATCH", "/http_mergepatch_ct_test/"+id, map[string]any{"status": "done"},
		"If-Match", etag, "Content-Type", "application/json")
	if wrongCT.Status != http.StatusUnsupportedMediaType {
		t.Fatalf("PATCH with application/json = %d %s", wrongCT.Status, wrongCT.Raw)
	}

	rightCT := c.Do("PATCH", "/http_mergepatch_ct_test/"+id, map[string]any{"status": "done"},
		"If-Match", etag, "Content-Type", "application/merge-patch+json")
	if rightCT.Status != http.StatusOK {
		t.Fatalf("PATCH with application/merge-patch+json = %d %s", rightCT.Status, rightCT.Raw)
	}

	// PUT is a full replace, not a merge patch -- never subject to this check.
	put := c.Do("PUT", "/http_mergepatch_ct_test/"+id, map[string]any{"name": "b"},
		"If-Match", rightCT.String("_etag"), "Content-Type", "application/json")
	if put.Status != http.StatusOK {
		t.Fatalf("PUT with application/json = %d %s", put.Status, put.Raw)
	}
}
