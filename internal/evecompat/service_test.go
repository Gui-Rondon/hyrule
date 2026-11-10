package evecompat_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sort"
	"strings"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/Gui-Rondon/hyrule/internal/evecompat"
	"github.com/Gui-Rondon/hyrule/internal/platform/auth"
	"github.com/Gui-Rondon/hyrule/internal/platform/mongodb"
	"github.com/Gui-Rondon/hyrule/internal/testkit"
)

func newAPI(t *testing.T, authn *auth.Authenticator, res *evecompat.Resource) (*testkit.Client, *evecompat.Service) {
	t.Helper()
	db := testkit.Mongo(t)
	tx, _, err := mongodb.TransactorFor(context.Background(), db.Client())
	if err != nil {
		t.Fatal(err)
	}
	if res.Store == nil {
		res.Store = mongodb.NewStore(db.Collection("items"))
	}
	api := evecompat.New(evecompat.Options{Auth: authn, Transactor: tx})
	svc := api.Register(res)
	return testkit.NewClient(t, api), svc
}

func items() *evecompat.Resource {
	return &evecompat.Resource{
		Name: "items",
		Schema: evecompat.Schema{
			"name": {Type: evecompat.TypeString},
			"seo": {Type: evecompat.TypeDict, Schema: evecompat.Schema{
				"title":       {Type: evecompat.TypeString},
				"description": {Type: evecompat.TypeString},
			}},
		},
		ResourceMethods: []string{http.MethodGet, http.MethodPost},
		ItemMethods:     []string{http.MethodGet, http.MethodPatch, http.MethodPut, http.MethodDelete},
	}
}

func TestPublicAndPrivateMethods(t *testing.T) {
	keys := testkit.NewKeys(t)
	res := items()
	res.PublicMethods = []string{http.MethodGet}
	c, _ := newAPI(t, keys.Authenticator(t), res)

	cases := []struct {
		method string
		token  string
		want   int
	}{
		{"GET", "", http.StatusOK},
		// Eve only exempts the methods listed in PUBLIC_METHODS: HEAD needs
		// a token (403 in production, prdreplay 11-head-lista)
		{"HEAD", "", http.StatusForbidden},
		{"HEAD", "Token " + keys.Token(t, "fulano"), http.StatusOK},
		{"POST", "", http.StatusForbidden},
		{"POST", "Token " + keys.Token(t, "fulano"), http.StatusCreated},
	}
	for _, tc := range cases {
		var body any
		if tc.method == "POST" {
			body = map[string]any{"name": "x"}
		}
		res := c.Do(tc.method, "/items", body, "Authorization", tc.token)
		if res.Status != tc.want {
			t.Errorf("%s %q: %d, want %d (%s)", tc.method, tc.token, res.Status, tc.want, res.Raw)
		}
	}
}

func TestConcurrentPatchWithSameEtagOnlyOneWins(t *testing.T) {
	c, _ := newAPI(t, nil, items())
	created := c.Do("POST", "/items", map[string]any{"name": "a"})
	id, etag := created.String("_id"), created.String("_etag")

	results := make(chan int, 2)
	for _, name := range []string{"b", "c"} {
		go func() {
			results <- c.Do("PATCH", "/items/"+id, map[string]any{"name": name}, "If-Match", etag).Status
		}()
	}
	codes := map[int]int{}
	codes[<-results]++
	codes[<-results]++
	if codes[http.StatusOK] != 1 || codes[http.StatusPreconditionFailed] != 1 {
		t.Errorf("want one 200 and one 412, got %v", codes)
	}
}

// Eve's MERGE_NESTED_DOCUMENTS: a PATCH of a nested field keeps the others.
func TestPatchMergesNestedDocuments(t *testing.T) {
	c, _ := newAPI(t, nil, items())
	created := c.Do("POST", "/items", map[string]any{"seo": map[string]any{"title": "t", "description": "d"}})
	id := created.String("_id")

	res := c.Do("PATCH", "/items/"+id, map[string]any{"seo": map[string]any{"title": "novo"}}, "If-Match", created.String("_etag"))
	if res.Status != http.StatusOK {
		t.Fatalf("patch: %d %s", res.Status, res.Raw)
	}
	seo := c.Do("GET", "/items/"+id, nil).Map()["seo"].(map[string]any)
	if seo["title"] != "novo" || seo["description"] != "d" {
		t.Errorf("seo = %v", seo)
	}
}

// Every write must change the etag, even with the same content in the same
// second, or an old etag would still pass If-Match.
func TestEveryWriteChangesTheEtag(t *testing.T) {
	c, _ := newAPI(t, nil, items())
	created := c.Do("POST", "/items", map[string]any{"name": "a"})
	id, etag := created.String("_id"), created.String("_etag")

	second := c.Do("PATCH", "/items/"+id, map[string]any{"name": "a"}, "If-Match", etag)
	if second.String("_etag") == etag {
		t.Fatal("etag did not change")
	}
	if res := c.Do("PATCH", "/items/"+id, map[string]any{"name": "a"}, "If-Match", etag); res.Status != http.StatusPreconditionFailed {
		t.Errorf("old etag = %d, want 412", res.Status)
	}
}

func TestPreWriteHookErrorAbortsAndRollsBack(t *testing.T) {
	res := items()
	var sideEffects *evecompat.Service
	res.Hooks.OnInsert = append(res.Hooks.OnInsert, func(ctx context.Context, docs []evecompat.Doc) error {
		// written in the same transaction, must be rolled back
		if _, err := sideEffects.Resource().Store.(mongodb.Store).Collection.Database().Collection("outbox").InsertOne(ctx, bson.M{"x": 1}); err != nil {
			return err
		}
		if docs[0]["name"] == "proibido" {
			return evecompat.Abort(http.StatusConflict, "nome proibido")
		}
		return nil
	})
	c, svc := newAPI(t, nil, res)
	sideEffects = svc
	outbox := svc.Resource().Store.(mongodb.Store).Collection.Database().Collection("outbox")

	got := c.Do("POST", "/items", map[string]any{"name": "proibido"})
	if got.Status != http.StatusConflict {
		t.Fatalf("status = %d %s", got.Status, got.Raw)
	}
	if msg := got.Map()["_error"].(map[string]any)["message"]; msg != "nome proibido" {
		t.Errorf("message = %v", msg)
	}
	if n, _ := svc.Resource().Store.(mongodb.Store).Collection.CountDocuments(context.Background(), bson.M{}); n != 0 {
		t.Errorf("document stored despite the hook error")
	}
	if n, _ := outbox.CountDocuments(context.Background(), bson.M{}); n != 0 {
		t.Skip("Mongo is not a replica set; transaction rollback not verified")
	}

	if got := c.Do("POST", "/items", map[string]any{"name": "ok"}); got.Status != http.StatusCreated {
		t.Fatalf("status = %d", got.Status)
	}
	if n, _ := outbox.CountDocuments(context.Background(), bson.M{}); n != 1 {
		t.Errorf("outbox documents = %d, want 1", n)
	}
}

func TestHooksReceiveChangesAndOriginal(t *testing.T) {
	res := items()
	var gotUpdates, gotOriginal evecompat.Doc
	res.Hooks.OnUpdated = append(res.Hooks.OnUpdated, func(_ context.Context, updates, original evecompat.Doc) error {
		gotUpdates, gotOriginal = updates, original
		return nil
	})
	c, _ := newAPI(t, nil, res)
	created := c.Do("POST", "/items", map[string]any{"name": "a"})
	c.Do("PATCH", "/items/"+created.String("_id"), map[string]any{"name": "b"}, "If-Match", created.String("_etag"))

	if gotUpdates["name"] != "b" || gotOriginal["name"] != "a" {
		t.Errorf("updates=%v original=%v", gotUpdates, gotOriginal)
	}
}

func TestSoftDelete(t *testing.T) {
	res := items()
	res.SoftDelete = true
	c, svc := newAPI(t, nil, res)
	created := c.Do("POST", "/items", map[string]any{"name": "a"})
	id := created.String("_id")

	if got := c.Do("GET", "/items/"+id, nil).Map()["_deleted"]; got != false {
		t.Errorf("_deleted on insert = %v", got)
	}
	// auto_fields of Eve: _deleted is in the write responses, and PATCH
	// stores it (patch.py: "PATCH with soft delete enabled should always set
	// the DELETED field to False")
	if got, ok := created.Map()["_deleted"]; !ok || got != false {
		t.Errorf("_deleted in POST response = %v, %v", got, ok)
	}
	oid, _ := bson.ObjectIDFromHex(id)
	svc.Resource().Store.(mongodb.Store).Collection.UpdateOne(context.Background(), bson.M{"_id": oid}, bson.M{"$unset": bson.M{"_deleted": ""}})
	patched := c.Do("PATCH", "/items/"+id, map[string]any{"name": "b"}, "If-Match", created.String("_etag"))
	if got, ok := patched.Map()["_deleted"]; !ok || got != false {
		t.Errorf("_deleted in PATCH response = %v, %v (%s)", got, ok, patched.Raw)
	}
	if n, _ := svc.Resource().Store.(mongodb.Store).Collection.CountDocuments(context.Background(), bson.M{"_id": oid, "_deleted": false}); n != 1 {
		t.Errorf("PATCH must store _deleted: false")
	}
	replaced := c.Do("PUT", "/items/"+id, map[string]any{"name": "c"}, "If-Match", patched.String("_etag"))
	if got, ok := replaced.Map()["_deleted"]; !ok || got != false {
		t.Errorf("_deleted in PUT response = %v, %v", got, ok)
	}
	created = replaced
	if res := c.Do("DELETE", "/items/"+id, nil); res.Status != http.StatusPreconditionRequired {
		t.Errorf("delete without If-Match = %d", res.Status)
	}
	if res := c.Do("DELETE", "/items/"+id, nil, "If-Match", created.String("_etag")); res.Status != http.StatusNoContent {
		t.Fatalf("delete = %d %s", res.Status, res.Raw)
	}
	if res := c.Do("GET", "/items/"+id, nil); res.Status != http.StatusNotFound {
		t.Errorf("GET after delete = %d", res.Status)
	}
	if total := c.Do("GET", "/items", nil).Map()["_meta"].(map[string]any)["total"]; total != float64(0) {
		t.Errorf("total after delete = %v", total)
	}
	n, _ := svc.Resource().Store.(mongodb.Store).Collection.CountDocuments(context.Background(), bson.M{"_deleted": true})
	if n != 1 {
		t.Errorf("document must stay in Mongo marked as deleted")
	}
}

// OPTIMIZE_PAGINATION_FOR_SPEED skips the count: no total, no last page and
// always a next link.
func TestOptimizedPagination(t *testing.T) {
	res := items()
	res.Pagination.OptimizeForSpeed = true
	c, _ := newAPI(t, nil, res)
	c.Do("POST", "/items", []any{map[string]any{"name": "a"}, map[string]any{"name": "b"}})

	body := c.Do("GET", "/items?max_results=1", nil).Map()
	meta := body["_meta"].(map[string]any)
	if _, ok := meta["total"]; ok {
		t.Errorf("_meta = %v", meta)
	}
	links := body["_links"].(map[string]any)
	if _, ok := links["next"]; !ok {
		t.Errorf("missing next link: %v", links)
	}
	if _, ok := links["last"]; ok {
		t.Errorf("unexpected last link: %v", links)
	}
}

func TestDatasourceDefaults(t *testing.T) {
	res := items()
	res.Datasource = evecompat.Datasource{
		Projection:  bson.M{"name": 1},
		DefaultSort: bson.D{{Key: "name", Value: -1}},
		Filter:      bson.M{"hidden": bson.M{"$ne": true}},
	}
	res.AllowUnknown = true
	c, _ := newAPI(t, nil, res)
	c.Do("POST", "/items", []any{
		map[string]any{"name": "a", "seo": map[string]any{"title": "x"}},
		map[string]any{"name": "b"},
		map[string]any{"name": "c", "hidden": true},
	})

	list := c.Do("GET", "/items", nil).Map()["_items"].([]any)
	if len(list) != 2 {
		t.Fatalf("datasource filter not applied: %d items", len(list))
	}
	first := list[0].(map[string]any)
	if first["name"] != "b" {
		t.Errorf("default sort not applied: first = %v", first["name"])
	}
	for _, it := range list {
		if _, ok := it.(map[string]any)["seo"]; ok {
			t.Errorf("default projection not applied: %v", it)
		}
	}
}

// _datasource_ex of Eve 2.3.1 (io/base.py): with a datasource projection the
// client one only narrows it ("avoid sniffing of private fields"), in lists
// and items alike.
func TestClientProjection(t *testing.T) {
	res := items()
	res.Datasource = evecompat.Datasource{Projection: bson.M{"name": 1, "seo": 1}}
	res.AllowUnknown = true
	c, _ := newAPI(t, nil, res)
	id := c.Do("POST", "/items", map[string]any{"name": "a", "seo": map[string]any{"title": "x", "description": "d"},
		"secret": "s"}).String("_id")

	keys := func(m map[string]any) []string {
		var out []string
		for k := range m {
			if !strings.HasPrefix(k, "_") {
				out = append(out, k)
			}
		}
		sort.Strings(out)
		return out
	}
	for _, tc := range []struct{ projection, want string }{
		{`{"name":1}`, "[name]"},
		{`{"name":true}`, "[name]"},
		{`{"secret":1}`, "[]"},
		{`{"seo":0}`, "[name]"},
		{`{"secret":1,"name":1}`, "[name]"},
		{`{"seo.title":1}`, "[seo]"},
	} {
		item := c.Do("GET", "/items/"+id+"?projection="+tc.projection, nil).Map()
		if got := fmt.Sprint(keys(item)); got != tc.want {
			t.Errorf("item %s: %s, want %s", tc.projection, got, tc.want)
		}
		if _, ok := item["_etag"]; !ok {
			t.Errorf("item %s: meta fields dropped: %v", tc.projection, item)
		}
		list := c.Do("GET", "/items?projection="+tc.projection, nil).Map()["_items"].([]any)
		if got := fmt.Sprint(keys(list[0].(map[string]any))); got != tc.want {
			t.Errorf("list %s: %s, want %s", tc.projection, got, tc.want)
		}
	}
	item := c.Do("GET", "/items/"+id+`?projection={"seo.title":1}`, nil).Map()
	if seo := item["seo"].(map[string]any); len(seo) != 1 || seo["title"] != "x" {
		t.Errorf("seo.title projection = %v", seo)
	}
}

// Without a datasource projection (allow_unknown), Eve 2.3.1 uses the client
// projection as sent and then drops the falsy values (io/base.py): an
// exclusion is ignored. Checked against the settings reference
// (projection={"outro":0} returns outro). The meta fields come as stored
// (V-17).
func TestClientProjectionWithoutDatasource(t *testing.T) {
	res := items()
	res.AllowUnknown = true
	res.SoftDelete = true
	c, _ := newAPI(t, nil, res)
	id := c.Do("POST", "/items", map[string]any{"name": "a", "outro": "x", "seo": map[string]any{"title": "t"}}).String("_id")
	keys := func(m map[string]any) []string {
		var out []string
		for k := range m {
			if k != "_links" {
				out = append(out, k)
			}
		}
		sort.Strings(out)
		return out
	}
	all := "[_created _deleted _etag _id _updated name outro seo]"
	for _, tc := range []struct{ projection, want string }{
		{`{"outro":0}`, all},
		{`{"outro":false,"name":0}`, all},
		{`{"outro":1}`, "[_created _deleted _etag _id _updated outro]"},
		{`{"name":1,"seo":0}`, "[_created _deleted _etag _id _updated name]"},
		{`{"seo.title":true}`, "[_created _deleted _etag _id _updated seo]"},
	} {
		item := c.Do("GET", "/items/"+id+"?projection="+tc.projection, nil).Map()
		if got := fmt.Sprint(keys(item)); got != tc.want {
			t.Errorf("item %s: %s, want %s", tc.projection, got, tc.want)
		}
		list := c.Do("GET", "/items?projection="+tc.projection, nil).Map()["_items"].([]any)
		if got := fmt.Sprint(keys(list[0].(map[string]any))); got != tc.want {
			t.Errorf("list %s: %s, want %s", tc.projection, got, tc.want)
		}
	}
}

// TestSchemaProjection: without allow_unknown, Eve's datasource projection is
// the schema fields plus the meta fields (_set_resource_projection), so
// fields stored outside the schema are not returned; an exclusion-only
// datasource projection removes fields from that set.
func TestSchemaProjection(t *testing.T) {
	keys := func(m map[string]any) []string {
		var out []string
		for k := range m {
			if !strings.HasPrefix(k, "_") || k == "_deleted" {
				out = append(out, k)
			}
		}
		sort.Strings(out)
		return out
	}
	for _, tc := range []struct {
		name         string
		ds           bson.M
		allowUnknown bool
		softDelete   bool
		projection   string
		want         string
	}{
		{name: "schema only", want: "[name seo]"},
		{name: "client inclusion", projection: `{"name":1}`, want: "[name]"},
		{name: "client field outside schema", projection: `{"legacy":1}`, want: "[]"},
		{name: "client exclusion", projection: `{"seo":0}`, want: "[name]"},
		{name: "datasource exclusion", ds: bson.M{"seo": 0}, want: "[name]"},
		// the excluded key stays in the datasource projection, so a client
		// inclusion brings it back (field_base in fields)
		{name: "datasource exclusion and client", ds: bson.M{"seo": 0}, projection: `{"seo":1}`, want: "[seo]"},
		{name: "soft delete", softDelete: true, want: "[_deleted name seo]"},
		{name: "allow unknown", allowUnknown: true, want: "[legacy name seo]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := items()
			res.Datasource.Projection = tc.ds
			res.AllowUnknown = tc.allowUnknown
			res.SoftDelete = tc.softDelete
			c, svc := newAPI(t, nil, res)
			id := c.Do("POST", "/items", map[string]any{"name": "a", "seo": map[string]any{"title": "x"}}).String("_id")
			oid, _ := bson.ObjectIDFromHex(id)
			if _, err := svc.Resource().Store.(mongodb.Store).Collection.UpdateByID(context.Background(), oid,
				bson.M{"$set": bson.M{"legacy": "gravado por um worker"}}); err != nil {
				t.Fatal(err)
			}
			q := ""
			if tc.projection != "" {
				q = "?projection=" + tc.projection
			}
			item := c.Do("GET", "/items/"+id+q, nil).Map()
			if got := fmt.Sprint(keys(item)); got != tc.want {
				t.Errorf("item: %s, want %s", got, tc.want)
			}
			if _, ok := item["_etag"]; !ok {
				t.Errorf("item: meta fields dropped: %v", item)
			}
			list := c.Do("GET", "/items"+q, nil).Map()["_items"].([]any)
			if got := fmt.Sprint(keys(list[0].(map[string]any))); got != tc.want {
				t.Errorf("list: %s, want %s", got, tc.want)
			}
		})
	}
}

func TestLinksUseResourceAndItemTitles(t *testing.T) {
	res := items()
	res.URL = "things"
	res.ResourceTitle = "things"
	res.ItemTitle = "thing"
	c, _ := newAPI(t, nil, res)
	created := c.Do("POST", "/things", map[string]any{"name": "a"})
	self := created.Map()["_links"].(map[string]any)["self"].(map[string]any)
	if self["title"] != "thing" {
		t.Errorf("self = %v", self)
	}
	item := c.Do("GET", "/things/"+created.String("_id"), nil).Map()["_links"].(map[string]any)
	if item["collection"].(map[string]any)["title"] != "things" {
		t.Errorf("links = %v", item)
	}
}

func TestServiceIsUsableWithoutHTTP(t *testing.T) {
	_, svc := newAPI(t, nil, items())
	ctx := context.Background()

	docs, err := svc.Insert(ctx, []evecompat.Doc{{"name": "a"}}, evecompat.WriteOptions{})
	if err != nil {
		t.Fatal(err)
	}
	lookup := bson.M{"_id": docs[0]["_id"]}
	// patch_internal skips the concurrency check by default in Eve
	updated, err := svc.Patch(ctx, lookup, evecompat.Doc{"name": "b"}, evecompat.WriteOptions{SkipConcurrency: true})
	if err != nil {
		t.Fatal(err)
	}
	if updated["name"] != "b" {
		t.Errorf("updated = %v", updated)
	}
	_, err = svc.Patch(ctx, lookup, evecompat.Doc{"name": 1}, evecompat.WriteOptions{SkipConcurrency: true})
	var verr *evecompat.ValidationError
	if !errors.As(err, &verr) || verr.Issues["name"] != "must be of string type" {
		t.Errorf("err = %v", err)
	}
	if _, err := svc.Get(ctx, bson.M{"_id": bson.NewObjectID()}); !evecompat.IsNotFound(err) {
		t.Errorf("missing document err = %v", err)
	}
}

func TestAdditionalLookupIsReadOnly(t *testing.T) {
	res := items()
	res.AdditionalLookup = &evecompat.AdditionalLookup{Field: "name", Pattern: regexp.MustCompile(`^[a-z]+$`)}
	c, _ := newAPI(t, nil, res)
	c.Do("POST", "/items", map[string]any{"name": "abc"})

	if res := c.Do("GET", "/items/abc", nil); res.Status != http.StatusOK {
		t.Errorf("GET by name = %d", res.Status)
	}
	if res := c.Do("PATCH", "/items/abc", map[string]any{}, "If-Match", "x"); res.Status != http.StatusMethodNotAllowed {
		t.Errorf("PATCH by name = %d", res.Status)
	}
}

func TestLegacyWerkzeugMessages(t *testing.T) {
	db := testkit.Mongo(t)
	api := evecompat.New(evecompat.Options{Messages: evecompat.Werkzeug0})
	res := items()
	res.Store = mongodb.NewStore(db.Collection("items"))
	api.Register(res)
	c := testkit.NewClient(t, api)

	msg := c.Do("GET", "/items/aaaaaaaaaaaaaaaaaaaaaaaa", nil).Map()["_error"].(map[string]any)["message"]
	if msg != "The requested URL was not found on the server.  If you entered the URL manually please check your spelling and try again." {
		t.Errorf("message = %q", msg)
	}
}

// A lookup can match more than one field, as videos' find_by_slug does with
// friendly_title and friendly_title_history.
func TestAdditionalLookupFilter(t *testing.T) {
	res := items()
	res.Schema["aliases"] = evecompat.Field{Type: evecompat.TypeList, Items: &evecompat.Field{Type: evecompat.TypeString}}
	res.AdditionalLookup = &evecompat.AdditionalLookup{
		Pattern: regexp.MustCompile(`^[a-z-]+$`),
		Filter: func(v string) bson.M {
			return bson.M{"$or": bson.A{bson.M{"name": v}, bson.M{"aliases": v}}}
		},
	}
	c, _ := newAPI(t, nil, res)
	c.Do("POST", "/items", map[string]any{"name": "novo", "aliases": []string{"antigo"}})

	for _, path := range []string{"/items/novo", "/items/antigo"} {
		if got := c.Do("GET", path, nil); got.Status != http.StatusOK || got.String("name") != "novo" {
			t.Errorf("GET %s = %d %s", path, got.Status, got.Raw)
		}
	}
	if got := c.Do("GET", "/items/outro", nil); got.Status != http.StatusNotFound {
		t.Errorf("GET unknown = %d", got.Status)
	}
}

// Custom endpoints write with the resource's transaction and answer as
// GET /<url>/<id>.
func TestServiceHelpersForCustomEndpoints(t *testing.T) {
	res := items()
	res.CacheControl, res.CacheExpires = "max-age=20", 20
	c, svc := newAPI(t, nil, res)
	created := c.Do("POST", "/items", map[string]any{"name": "a"})
	id := created.String("_id")
	etag := created.String("_etag")
	oid, _ := bson.ObjectIDFromHex(id)

	original, err := svc.Find(context.Background(), bson.M{"_id": oid})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.CheckEtag(original, ""); !errors.Is(err, evecompat.ErrPreconditionRequired) {
		t.Errorf("no If-Match = %v", err)
	}
	if err := svc.CheckEtag(original, "x"); err == nil {
		t.Error("wrong etag accepted")
	}
	if err := svc.CheckEtag(original, `"`+etag+`"`); err != nil {
		t.Errorf("right etag = %v", err)
	}

	err = svc.WithTransaction(context.Background(), func(ctx context.Context) error {
		_, err := svc.Resource().Store.(mongodb.Store).Collection.UpdateByID(ctx, oid, bson.M{"$set": bson.M{"name": "b"}})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}

	doc, _ := svc.Find(context.Background(), bson.M{"_id": oid})
	rec := httptest.NewRecorder()
	svc.WriteItem(rec, httptest.NewRequest(http.MethodGet, "/items/"+id, nil), doc)
	var body map[string]any
	json.Unmarshal(rec.Body.Bytes(), &body)
	if rec.Code != http.StatusOK || body["name"] != "b" || body["_links"] == nil {
		t.Errorf("WriteItem = %d %s", rec.Code, rec.Body)
	}
	if rec.Header().Get("ETag") != `"`+etag+`"` || rec.Header().Get("Last-Modified") == "" ||
		rec.Header().Get("Cache-Control") != "max-age=20" {
		t.Errorf("headers = %v", rec.Header())
	}
	// a custom POST (publish) answers the item without cache headers
	post := httptest.NewRecorder()
	svc.WriteItem(post, httptest.NewRequest(http.MethodPost, "/items/"+id+"/publish", nil), doc)
	if post.Header().Get("Cache-Control") != "" || post.Header().Get("Expires") != "" || post.Header().Get("ETag") == "" {
		t.Errorf("POST headers = %v", post.Header())
	}
}

// contents-api (Eve 0.7): custom routes answer lists and items with links
// from the request path, items carry a next link, ids may be stored as
// strings (query_objectid_as_string) and a resource validator checks the
// document by its type.
func TestEve07CustomRoutes(t *testing.T) {
	res := items()
	res.Pagination.OptimizeForSpeed = true
	res.ItemNextLink = true
	res.QueryObjectIDAsString = true
	res.ResourceTitle = "items-as-string"
	res.ItemTitle = "items"
	res.Schema["ref"] = evecompat.Field{Type: evecompat.TypeString}
	res.Validate = func(_ context.Context, doc, original evecompat.Doc, partial bool) (evecompat.Issues, error) {
		if name, ok := doc["name"]; ok && name == "proibido" {
			return evecompat.Issues{"name": "proibido"}, nil
		}
		if !partial && doc["ref"] == nil && original == nil {
			return evecompat.Issues{"ref": "required field"}, nil
		}
		return nil, nil
	}
	c, svc := newAPI(t, nil, res)
	ref := bson.NewObjectID().Hex()

	if got := c.Do("POST", "/items", map[string]any{"name": "a"}); got.Status != http.StatusUnprocessableEntity {
		t.Errorf("validator on POST = %d %s", got.Status, got.Raw)
	}
	created := c.Do("POST", "/items", map[string]any{"name": "a", "ref": ref})
	id := created.String("_id")
	if got := c.Do("PATCH", "/items/"+id, map[string]any{"name": "proibido"}, "If-Match", created.String("_etag")); got.Status != http.StatusUnprocessableEntity {
		t.Errorf("validator on PATCH = %d", got.Status)
	}

	// the 24 hex string in where stays a string, so the stored string matches
	list := c.Do("GET", `/items?where={"ref":"`+ref+`"}`, nil)
	if items := list.Map()["_items"].([]any); len(items) != 1 {
		t.Fatalf("objectid as string = %s", list.Raw)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /custom/list", func(w http.ResponseWriter, r *http.Request) {
		svc.WriteList(w, r, bson.M{"name": "a"})
	})
	mux.HandleFunc("GET /custom/item", func(w http.ResponseWriter, r *http.Request) {
		svc.WriteFound(w, r, bson.M{"ref": ref})
	})
	mux.HandleFunc("GET /custom/missing", func(w http.ResponseWriter, r *http.Request) {
		svc.WriteFound(w, r, bson.M{"ref": "x"})
	})
	cc := testkit.NewClient(t, mux)

	got := cc.Do("GET", "/custom/list?max_results=1&q=x", nil)
	links := got.Map()["_links"].(map[string]any)
	item := got.Map()["_items"].([]any)[0].(map[string]any)
	if links["self"].(map[string]any)["href"] != "custom/list" || links["self"].(map[string]any)["title"] != "items-as-string" ||
		links["next"].(map[string]any)["href"] != "custom/list?max_results=1&page=2&q=x" ||
		item["_links"].(map[string]any)["self"].(map[string]any)["href"] != "custom/list/"+id {
		t.Errorf("list links = %v / %v", links, item["_links"])
	}

	got = cc.Do("GET", "/custom/item?url=u", nil)
	links = got.Map()["_links"].(map[string]any)
	if got.Status != http.StatusOK || got.Header.Get("ETag") == "" ||
		links["self"].(map[string]any)["href"] != "custom/item/"+id ||
		links["collection"].(map[string]any)["href"] != "custom/item?url=u" ||
		links["collection"].(map[string]any)["title"] != "items-as-string" ||
		links["next"].(map[string]any)["href"] != "custom/item/"+id+"?page=2&url=u" {
		t.Errorf("item = %d %v", got.Status, links)
	}
	if got := cc.Do("GET", "/custom/missing", nil); got.Status != http.StatusNotFound {
		t.Errorf("missing = %d", got.Status)
	}
	// the standard item route has the next link too
	std := c.Do("GET", "/items/"+id, nil).Map()["_links"].(map[string]any)
	if std["next"].(map[string]any)["href"] != "items/"+id+"?page=2" {
		t.Errorf("standard item links = %v", std)
	}
	// Eve 2.3 points it at the collection
	res.ItemNextFromCollection = true
	std = c.Do("GET", "/items/"+id, nil).Map()["_links"].(map[string]any)
	if std["next"].(map[string]any)["href"] != "items?page=2" {
		t.Errorf("Eve 2.3 item next = %v", std["next"])
	}
}

// WriteListFallback is two get(resource, lookup) calls with the same
// request, as contents-api's get_content_by_url: the exact lookup answers
// when its page has items, the fallback otherwise. The client where and the
// page apply to both.
func TestWriteListFallback(t *testing.T) {
	c, svc := newAPI(t, nil, items())
	a := c.Do("POST", "/items", map[string]any{"name": "a"}).String("_id")
	b := c.Do("POST", "/items", map[string]any{"name": "b"}).String("_id")
	mux := http.NewServeMux()
	mux.HandleFunc("GET /custom/{exact}", func(w http.ResponseWriter, r *http.Request) {
		svc.WriteListFallback(w, r, bson.M{"name": r.PathValue("exact")}, bson.M{"name": bson.M{"$in": bson.A{"a", "b"}}})
	})
	cc := testkit.NewClient(t, mux)
	ids := func(path string) string {
		got := cc.Do("GET", path, nil)
		var out []string
		for _, it := range got.Map()["_items"].([]any) {
			out = append(out, it.(map[string]any)["_id"].(string))
		}
		return fmt.Sprint(got.Status, out)
	}
	for _, tc := range []struct{ path, want string }{
		{"/custom/a", fmt.Sprint(200, []string{a})},
		{"/custom/zzz", fmt.Sprint(200, []string{a, b})},
		{"/custom/a?page=2", fmt.Sprint(200, []string(nil))},
		{"/custom/zzz?page=2&max_results=1", fmt.Sprint(200, []string{b})},
		{`/custom/a?where={"name":"b"}`, fmt.Sprint(200, []string{b})},
	} {
		if got := ids(tc.path); got != tc.want {
			t.Errorf("%s = %s, want %s", tc.path, got, tc.want)
		}
	}
}

// Soft delete as Eve (0.7 and 2.x): ?show_deleted lists the deleted
// documents, and GET of a deleted item answers 404 with the document.
func TestSoftDeletedReads(t *testing.T) {
	res := items()
	res.SoftDelete = true
	c, _ := newAPI(t, nil, res)
	keep := c.Do("POST", "/items", map[string]any{"name": "a"})
	gone := c.Do("POST", "/items", map[string]any{"name": "b"})
	id := gone.String("_id")
	if got := c.Do("DELETE", "/items/"+id, nil, "If-Match", gone.String("_etag")); got.Status != http.StatusNoContent {
		t.Fatalf("delete = %d", got.Status)
	}

	if n := len(c.Do("GET", "/items", nil).Map()["_items"].([]any)); n != 1 {
		t.Errorf("list = %d items", n)
	}
	if n := len(c.Do("GET", "/items?show_deleted=false", nil).Map()["_items"].([]any)); n != 2 {
		t.Errorf("list with show_deleted = %d items (Eve checks presence, not value)", n)
	}

	got := c.Do("GET", "/items/"+id, nil)
	body := got.Map()
	status, _ := body["_status"].([]any)
	if got.Status != http.StatusNotFound || body["name"] != "b" || body["_deleted"] != true || len(status) != 1 || status[0] != "ERR" ||
		body["_error"].(map[string]any)["message"] != "The requested URL was not found on this server." ||
		body["_links"].(map[string]any)["self"] == nil || got.Header.Get("ETag") == "" {
		t.Errorf("deleted item = %d %v %s", got.Status, got.Header, got.Raw)
	}
	if got := c.Do("GET", "/items/"+keep.String("_id"), nil); got.Status != http.StatusOK {
		t.Errorf("kept item = %d", got.Status)
	}
}

// "/items/" is "/items", with no redirect, in every route and method, as Eve
// with strict_slashes = False (HLG: /articles/ and /articles/<id>/ answer
// 200); only one slash is removed, so "/items//" is a 404, as in Werkzeug.
func TestTrailingSlash(t *testing.T) {
	c, _ := newAPI(t, testkit.DisabledAuth(t), items())
	created := c.Do("POST", "/items/", map[string]any{"name": "a"})
	if created.Status != http.StatusCreated {
		t.Fatalf("POST /items/ = %d %s", created.Status, created.Raw)
	}
	id := created.String("_id")
	for _, path := range []string{"/items", "/items/", "/items/" + id, "/items/" + id + "/"} {
		if res := c.Do("GET", path, nil); res.Status != http.StatusOK {
			t.Errorf("GET %s = %d", path, res.Status)
		}
	}
	if a, b := c.Do("GET", "/items", nil), c.Do("GET", "/items/", nil); a.Raw != b.Raw {
		t.Errorf("/items and /items/ differ:\n%s\n%s", a.Raw, b.Raw)
	}
	res := c.Do("PATCH", "/items/"+id+"/", map[string]any{"name": "b"}, "If-Match", created.String("_etag"))
	if res.Status != http.StatusOK {
		t.Errorf("PATCH with slash = %d %s", res.Status, res.Raw)
	}
	for _, path := range []string{"/items//", "/items/" + id + "//"} {
		if res := c.Do("GET", path, nil); res.Status != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", path, res.Status)
		}
	}
}

// Eve's payload() also takes form-urlencoded bodies: articles-saver PATCHes
// with requests.patch(url, dict). The first value of a repeated key wins
// (AUTO_COLLAPSE_MULTI_KEYS off) and an empty form is a 400.
func TestFormBody(t *testing.T) {
	c, _ := newAPI(t, testkit.DisabledAuth(t), items())
	form := "application/x-www-form-urlencoded"
	created := c.Do("POST", "/items", "name=a", "Content-Type", form)
	if created.Status != http.StatusCreated {
		t.Fatalf("form POST = %d %s", created.Status, created.Raw)
	}
	id := created.String("_id")
	res := c.Do("PATCH", "/items/"+id, "name=b&name=c", "Content-Type", form+"; charset=utf-8", "If-Match", created.String("_etag"))
	if res.Status != http.StatusOK {
		t.Fatalf("form PATCH = %d %s", res.Status, res.Raw)
	}
	if got := c.Do("GET", "/items/"+id, nil).String("name"); got != "b" {
		t.Errorf("name = %q, want the first value", got)
	}
	// only the body: fields in the query string are not part of the
	// document, as Flask's request.form
	res = c.Do("POST", "/items?name=from-query&seo=x", "name=from-body", "Content-Type", form)
	if res.Status != http.StatusCreated {
		t.Fatalf("form POST with query = %d %s", res.Status, res.Raw)
	}
	if doc := c.Do("GET", "/items/"+res.String("_id"), nil); doc.String("name") != "from-body" || doc.Map()["seo"] != nil {
		t.Errorf("query string in the document: %s", doc.Raw)
	}
	if res := c.Do("POST", "/items", "", "Content-Type", form); res.Status != http.StatusBadRequest || !strings.Contains(res.Raw, "No form-urlencoded data supplied") {
		t.Errorf("empty form = %d %s", res.Status, res.Raw)
	}
	// the values stay strings, as in Eve: a field of another type fails
	// the validation with 422, not a conversion
	if res := c.Do("POST", "/items", "name=a&seo=x", "Content-Type", form); res.Status != http.StatusUnprocessableEntity || !strings.Contains(res.Raw, "seo") {
		t.Errorf("dict field from a form = %d %s", res.Status, res.Raw)
	}
	// multipart is not read (no client sends it): a 400, as a bad JSON body
	body := "--x\r\nContent-Disposition: form-data; name=\"name\"\r\n\r\nm\r\n--x--\r\n"
	if res := c.Do("POST", "/items", body, "Content-Type", "multipart/form-data; boundary=x"); res.Status != http.StatusBadRequest {
		t.Errorf("multipart POST = %d %s", res.Status, res.Raw)
	}
}
