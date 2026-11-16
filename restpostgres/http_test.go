package restpostgres

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/Gui-Rondon/hyrule/platform/postgres"
	"github.com/Gui-Rondon/hyrule/rest"
	"github.com/Gui-Rondon/hyrule/testkit"
)

// defaultHTTPTestSchema is the schema every existing test below used to
// have hardcoded inside newHTTPTestAPI -- named and factored out so a
// test that needs a different field (e.g. TestHTTPDateTimeFilter...'s
// published_at) can pass its own schema instead.
var defaultHTTPTestSchema = rest.Schema{
	"name":   {Type: rest.TypeString, Required: true},
	"status": {Type: rest.TypeString},
}

// newHTTPTestAPI wires a real postgresAdapter into a real rest.API and
// drives it over HTTP (testkit.NewClient) -- Fase 4b's actual deliverable:
// before it, no HTTP request could reach a Postgres resource at all
// (ParseListQuery/ListQuery stayed bson.M/bson.D-typed, and itemLookup/
// checkMatched hardcoded a Mongo bson.M{"_id": id} filter that
// postgresAdapter's CombineFilter/compileFilter couldn't compile).
func newHTTPTestAPI(t *testing.T, table string, schema rest.Schema, softDelete bool, configure func(*rest.Resource)) *testkit.Client {
	t.Helper()
	pool := testPool(t)
	setupResourceTable(t, pool, table)
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
	c := newHTTPTestAPI(t, "http_crud_test", defaultHTTPTestSchema, false, nil)

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
	c := newHTTPTestAPI(t, "http_softdelete_test", defaultHTTPTestSchema, true, nil)

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
	c := newHTTPTestAPI(t, "http_mergepatch_ct_test", defaultHTTPTestSchema, false, func(res *rest.Resource) {
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

// TestHTTPDateTimeFilterComparesByInstantNotText is Fase 4's still-open
// inspection pendency (plan section 16/23): hyrule_safe_timestamptz
// (Fase 1) and the $eq/$gt/$in-by-instant fix (Fase 3, PR #15) were
// only ever exercised at platform/postgres's compiler level
// (query_integration_test.go), never through a real rest.Resource/HTTP
// request. EnsureFunctions is never called by NewAdapter/NewService on
// their own (confirmed by grep across the whole repo) -- a resource
// with a KindDateTime field needs it run once, as a schema-setup step,
// same as the CREATE TABLE/CREATE INDEX this test's own setup already
// runs via setupResourceTable.
func TestHTTPDateTimeFilterComparesByInstantNotText(t *testing.T) {
	pool := testPool(t)
	if err := postgres.EnsureFunctions(context.Background(), pool); err != nil {
		t.Fatal(err)
	}
	schema := rest.Schema{
		"name":         {Type: rest.TypeString, Required: true},
		"published_at": {Type: rest.TypeDatetime},
	}
	c := newHTTPTestAPI(t, "http_datetime_test", schema, false, nil)

	// rest.TypeDatetime's own wire format (checkType/convert, via
	// parseDate) is Eve's DATE_FORMAT, not ISO 8601 -- the body a POST
	// sends must use it, even though the where= filter below (decoded
	// by ParseWhere, which never runs it through the Schema at all)
	// and the stored jsonb value (a time.Time always marshals to RFC
	// 3339) are both ISO 8601.
	old := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	new_ := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	created1 := c.Do("POST", "/http_datetime_test", map[string]any{"name": "old", "published_at": old.Format(rest.DateFormat)})
	created2 := c.Do("POST", "/http_datetime_test", map[string]any{"name": "new", "published_at": new_.Format(rest.DateFormat)})
	if created1.Status != http.StatusCreated || created2.Status != http.StatusCreated {
		t.Fatalf("POST = %d %s / %d %s", created1.Status, created1.Raw, created2.Status, created2.Raw)
	}

	// $eq by the exact instant the client used ("Z" suffix) must match
	// -- before Fase 3's fix this always failed: to_jsonb() renders the
	// stored instant back with "+00:00", never "Z", so a textual
	// comparison never matched even the exact same instant.
	eq := c.Do("GET", `/http_datetime_test?where={"published_at":{"$eq":"2020-01-01T00:00:00Z"}}`, nil)
	eqItems, _ := eq.Map()["_items"].([]any)
	if eq.Status != http.StatusOK || len(eqItems) != 1 {
		t.Fatalf("GET with $eq on published_at = %d %v", eq.Status, eq.Map())
	}

	// $gt compares by real instant, never lexicographic text.
	gt := c.Do("GET", `/http_datetime_test?where={"published_at":{"$gt":"2021-01-01T00:00:00Z"}}`, nil)
	gtItems, _ := gt.Map()["_items"].([]any)
	if gt.Status != http.StatusOK || len(gtItems) != 1 || gtItems[0].(map[string]any)["name"] != "new" {
		t.Fatalf("GET with $gt on published_at = %d %v", gt.Status, gt.Map())
	}

	// $in must match by instant too -- Fase 3's second finding in the
	// same PR, found by /code-review rather than by a test until now:
	// $in/$nin still used the textual to_jsonb path even after the
	// $eq/$ne fix.
	in := c.Do("GET", `/http_datetime_test?where={"published_at":{"$in":["2020-01-01T00:00:00Z"]}}`, nil)
	inItems, _ := in.Map()["_items"].([]any)
	if in.Status != http.StatusOK || len(inItems) != 1 {
		t.Fatalf("GET with $in on published_at = %d %v", in.Status, in.Map())
	}
}

// TestHTTPPatchOnResourceWithTypedFieldsDoesNotRevalidateThem is a real
// end-to-end repro of a /code-review finding on this same PR: an
// earlier version of ValidatesMergedDocument's post-merge check ran a
// full type/format revalidation over the merged document, which 422'd
// on every single PATCH to a resource with a non-string Required
// field already set -- a touched TypeInteger field is int64 after
// prepareWrite's pre-merge convert(), and an untouched TypeDatetime
// field read back from Postgres is an RFC 3339 string, never Eve's
// DateFormat checkType's TypeDatetime case parses against. This test
// never exercised a PATCH at all (TestHTTPDateTimeFilterComparesByInstantNotText
// above only does POST/GET), which is exactly how the bug went
// unnoticed until /code-review.
func TestHTTPPatchOnResourceWithTypedFieldsDoesNotRevalidateThem(t *testing.T) {
	schema := rest.Schema{
		"name":         {Type: rest.TypeString, Required: true},
		"count":        {Type: rest.TypeInteger, Required: true},
		"published_at": {Type: rest.TypeDatetime, Required: true},
	}
	c := newHTTPTestAPI(t, "http_patch_typed_fields_test", schema, false, nil)

	created := c.Do("POST", "/http_patch_typed_fields_test", map[string]any{
		"name": "a", "count": 5, "published_at": time.Now().UTC().Format(rest.DateFormat),
	})
	if created.Status != http.StatusCreated {
		t.Fatalf("POST = %d %s", created.Status, created.Raw)
	}
	id, etag := created.String("_id"), created.String("_etag")

	// Touches only name -- count and published_at are untouched,
	// merged through from the stored document (already converted/
	// round-tripped, never Eve's wire format).
	patched := c.Do("PATCH", "/http_patch_typed_fields_test/"+id, map[string]any{"name": "b"}, "If-Match", etag)
	if patched.Status != http.StatusOK {
		t.Fatalf("PATCH = %d %s, want 200 (an untouched typed field must never be revalidated)", patched.Status, patched.Raw)
	}

	// A touched TypeInteger field (already int64 by the time it's
	// merged) must not be revalidated either.
	patched2 := c.Do("PATCH", "/http_patch_typed_fields_test/"+id, map[string]any{"count": 6}, "If-Match", patched.String("_etag"))
	if patched2.Status != http.StatusOK {
		t.Fatalf("PATCH count = %d %s, want 200", patched2.Status, patched2.Raw)
	}
}

// TestHTTPPatchValidatesMergedDocumentAfterNullDeletesRequiredField is
// item 1's real end-to-end proof, through the actual HTTP stack and a
// real postgresAdapter -- not just the fake-adapter unit tests in
// rest/patchmerge_internal_test.go. category is Required+Nullable: the
// key must always be present, but RFC 7396 has no way to express
// "store null" without deleting the key -- a PATCH that sends
// category: null therefore removes a required key from the merged
// document, which only ValidatesMergedDocument's post-merge check
// catches (section 9.4 of the Postgres portability plan).
func TestHTTPPatchValidatesMergedDocumentAfterNullDeletesRequiredField(t *testing.T) {
	schema := rest.Schema{
		"name":     {Type: rest.TypeString, Required: true},
		"category": {Type: rest.TypeString, Required: true, Nullable: true},
	}
	c := newHTTPTestAPI(t, "http_patch_merge_validation_test", schema, false, nil)

	created := c.Do("POST", "/http_patch_merge_validation_test", map[string]any{"name": "a", "category": "x"})
	if created.Status != http.StatusCreated {
		t.Fatalf("POST = %d %s", created.Status, created.Raw)
	}
	id, etag := created.String("_id"), created.String("_etag")

	patched := c.Do("PATCH", "/http_patch_merge_validation_test/"+id, map[string]any{"category": nil}, "If-Match", etag)
	if patched.Status != http.StatusUnprocessableEntity {
		t.Fatalf("PATCH category:null = %d %s, want 422", patched.Status, patched.Raw)
	}
	issues, _ := patched.Map()["_issues"].(map[string]any)
	if _, ok := issues["category"]; !ok {
		t.Fatalf("_issues = %v, want an issue for category", patched.Map())
	}

	// The document itself must be untouched: the merged-document check
	// fails before any write reaches the database.
	after := c.Do("GET", "/http_patch_merge_validation_test/"+id, nil)
	if after.Map()["category"] != "x" || after.String("_etag") != etag {
		t.Fatalf("GET after the rejected PATCH = %v, want category=x and _etag=%q unchanged", after.Map(), etag)
	}
}

// TestHTTPPatchValidatesMergedDocumentIgnoresHookAddedFields is a real
// end-to-end repro of a /code-review finding on this same PR:
// ValidatesMergedDocument's post-merge check saw the whole merged
// document, including any field an OnUpdate hook stamped that the
// Schema never declares -- exactly behaviors/audit.Hooks()'s
// _updated_user, with no AllowUnknown and no _updated_user in Schema,
// which is precisely what cmd/hyrule/templates/service/resource.go.tmpl
// (this project's own scaffold) wires up. Before the fix, every PATCH
// on a resource like this 422'd on its own hook-added field; this
// test pins the fix against a real postgresAdapter/HTTP round trip
// (jsonb included), not just the fake-adapter unit test in
// rest/patchmerge_internal_test.go.
func TestHTTPPatchValidatesMergedDocumentIgnoresHookAddedFields(t *testing.T) {
	schema := rest.Schema{"name": {Type: rest.TypeString, Required: true}} // no _updated_user
	c := newHTTPTestAPI(t, "http_patch_hook_field_test", schema, false, func(res *rest.Resource) {
		res.Hooks.OnUpdate = []func(context.Context, rest.Doc, rest.Doc) error{
			func(_ context.Context, applied, _ rest.Doc) error {
				applied["_updated_user"] = "fulano" // behaviors/audit.Hooks(), inlined
				return nil
			},
		}
	})

	created := c.Do("POST", "/http_patch_hook_field_test", map[string]any{"name": "a"})
	id, etag := created.String("_id"), created.String("_etag")

	patched := c.Do("PATCH", "/http_patch_hook_field_test/"+id, map[string]any{"name": "b"}, "If-Match", etag)
	if patched.Status != http.StatusOK {
		t.Fatalf("PATCH = %d %s, want 200 (a hook-added field must never be treated as unknown)", patched.Status, patched.Raw)
	}
}

// TestHTTPDryRunWriteCutStaysA500NotA503 is a real end-to-end repro of
// a second /code-review finding on this same PR: postgresAdapter's
// IsTimeout falls back to errors.Is(err, context.DeadlineExceeded) for
// an error it doesn't recognize by a real pgconn.PgError code --
// exactly what a write cut by Resource.DryRun's DryRunTimeout produces
// from inside a hook, the same scenario rest/dryrun_test.go's
// TestDryRunTimeout already pins to 500 for Mongo. This confirms
// Postgres gets the identical 500 for the identical event, via a real
// postgresAdapter and a real pgx call racing the same expired context,
// not a guess about what pgx returns when its context is already done.
func TestHTTPDryRunWriteCutStaysA500NotA503(t *testing.T) {
	pool := testPool(t)
	table := "http_dryrun_write_cut_test"
	setupResourceTable(t, pool, table)
	schema := rest.Schema{"name": {Type: rest.TypeString, Required: true}}
	tx := postgres.Transactor{Pool: pool}
	adapter, err := NewAdapter(postgres.NewStore(pool, table), tx, Options{Schema: schema, Retry: rest.DefaultRetryOptions()})
	if err != nil {
		t.Fatal(err)
	}
	res := &rest.Resource{
		Name:            table,
		Schema:          schema,
		Adapter:         adapter,
		ResourceMethods: []string{http.MethodPost},
	}
	res.Hooks.OnInsert = []func(context.Context, []rest.Doc) error{
		func(ctx context.Context, _ []rest.Doc) error {
			<-ctx.Done()
			return nil
		},
	}
	api := rest.New(rest.Options{Transactor: tx, DryRun: true, DryRunTimeout: 50 * time.Millisecond})
	api.Register(res)
	c := testkit.NewClient(t, api)

	start := time.Now()
	out := c.Do("POST", "/"+table, map[string]any{"name": "a"})
	if out.Status != http.StatusInternalServerError || time.Since(start) > time.Second {
		t.Fatalf("dry run write cut = %d after %v, want 500", out.Status, time.Since(start))
	}
}
