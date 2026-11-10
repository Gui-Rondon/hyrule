package evecompat_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/Gui-Rondon/hyrule/internal/evecompat"
	"github.com/Gui-Rondon/hyrule/internal/platform/mongodb"
	"github.com/Gui-Rondon/hyrule/internal/testkit"
)

func TestHomeAndRouting(t *testing.T) {
	c, _ := newAPI(t, nil, items())
	home := c.Do("GET", "/", nil).Map()
	child := home["_links"].(map[string]any)["child"].([]any)[0].(map[string]any)
	if child["href"] != "items" || child["title"] != "items" {
		t.Errorf("home = %v", home)
	}
	if res := c.Do("GET", "/nada", nil); res.Status != http.StatusNotFound {
		t.Errorf("unknown path = %d", res.Status)
	}
	if res := c.Do("GET", "/items/nao-e-id", nil); res.Status != http.StatusNotFound {
		t.Errorf("lookup that is not an id = %d", res.Status)
	}
	if res := c.Do("DELETE", "/items", nil); res.Status != http.StatusMethodNotAllowed {
		t.Errorf("DELETE resource = %d", res.Status)
	}
	res := c.Do("OPTIONS", "/items", nil)
	if res.Header.Get("Allow") != "GET, POST, OPTIONS, HEAD" {
		t.Errorf("Allow = %q", res.Header.Get("Allow"))
	}
	// Flask's automatic OPTIONS answer
	if ct := res.Header.Get("Content-Type"); ct != "text/html; charset=utf-8" || res.Raw != "" {
		t.Errorf("OPTIONS Content-Type = %q, body %q", ct, res.Raw)
	}
}

// Production lists carry X-Total-Count and the Last-Modified of the newest
// item in the page (prdreplay 10-lista-max3).
func TestListHeaders(t *testing.T) {
	c, svc := newAPI(t, nil, items())
	coll := svc.Resource().Store.(mongodb.Store).Collection
	older := time.Date(2025, 10, 27, 17, 24, 20, 0, time.UTC)
	newer := time.Date(2026, 10, 22, 18, 23, 29, 0, time.UTC)
	coll.InsertMany(context.Background(), []any{
		bson.M{"name": "a", "_updated": older, "_created": older},
		bson.M{"name": "b", "_updated": newer, "_created": older},
		bson.M{"name": "c", "_updated": older, "_created": older},
	})

	res := c.Do("GET", "/items?max_results=2&sort=name", nil)
	if res.Header.Get("X-Total-Count") != "3" || res.Header.Get("Last-Modified") != "Thu, 22 Oct 2026 18:23:29 GMT" {
		t.Errorf("headers = %v", res.Header)
	}
	res = c.Do("GET", `/items?where={"name":"c"}`, nil)
	if res.Header.Get("X-Total-Count") != "1" || res.Header.Get("Last-Modified") != "Mon, 27 Oct 2025 17:24:20 GMT" {
		t.Errorf("filtered headers = %v", res.Header)
	}
	// channels-api in production: empty list, count 0, no Last-Modified
	res = c.Do("GET", `/items?where={"name":"nada"}`, nil)
	if res.Header.Get("X-Total-Count") != "0" || res.Header.Get("Last-Modified") != "" {
		t.Errorf("empty headers = %v", res.Header)
	}
}

// A stored document without _created/_updated (tag 6859b4c8… of HLG) is
// answered with the epoch, as Eve's build_response_document does; the
// _etag stays the one computed on the stored document.
func TestMissingDatesAnswerEpoch(t *testing.T) {
	c, svc := newAPI(t, nil, items())
	ins, err := svc.Resource().Store.(mongodb.Store).Collection.InsertOne(context.Background(), bson.M{"name": "sem datas"})
	if err != nil {
		t.Fatal(err)
	}
	id := ins.InsertedID.(bson.ObjectID).Hex()
	const epoch = "Thu, 01 Jan 1970 00:00:00 GMT"

	res := c.Do("GET", "/items/"+id, nil)
	item := res.Map()
	if item["_created"] != epoch || item["_updated"] != epoch {
		t.Errorf("item dates = %v %v", item["_created"], item["_updated"])
	}
	if res.Header.Get("Last-Modified") != epoch {
		t.Errorf("Last-Modified = %q", res.Header.Get("Last-Modified"))
	}
	etag := item["_etag"]
	list := c.Do("GET", "/items", nil).Map()
	first := list["_items"].([]any)[0].(map[string]any)
	if first["_created"] != epoch || first["_updated"] != epoch || first["_etag"] != etag {
		t.Errorf("list item = %v", first)
	}

	var stored bson.M
	svc.Resource().Store.(mongodb.Store).Collection.FindOne(context.Background(), bson.M{}).Decode(&stored)
	if _, ok := stored["_created"]; ok {
		t.Error("the stored document must not change")
	}
}

func TestListHeadersWithoutCount(t *testing.T) {
	res := items()
	res.Pagination.OptimizeForSpeed = true
	c, _ := newAPI(t, nil, res)
	c.Do("POST", "/items", map[string]any{"name": "a"})
	// articles-api: no total in _meta and no X-Total-Count
	if got := c.Do("GET", "/items", nil); got.Header.Get("X-Total-Count") != "" || got.Header.Get("Last-Modified") == "" {
		t.Errorf("optimized headers = %v", got.Header)
	}

	res = items()
	res.Pagination.Disabled = true
	c, _ = newAPI(t, nil, res)
	c.Do("POST", "/items", []any{map[string]any{"name": "a"}, map[string]any{"name": "b"}})
	if got := c.Do("GET", "/items", nil); got.Header.Get("X-Total-Count") != "2" {
		t.Errorf("unpaginated headers = %v", got.Header)
	}
}

// Eve's global CACHE_CONTROL/CACHE_EXPIRES also apply to the home endpoint
// (settings-api and channels-api in production, prdreplay 01-raiz).
func TestHomeCache(t *testing.T) {
	c, _ := newAPI(t, nil, items())
	if res := c.Do("GET", "/", nil); res.Header.Get("Cache-Control") != "" || res.Header.Get("Expires") != "" {
		t.Errorf("no global cache: %v", res.Header)
	}
	api := evecompat.New(evecompat.Options{CacheControl: "max-age=20", CacheExpires: 20})
	res := testkit.NewClient(t, api).Do("GET", "/", nil)
	exp, err := http.ParseTime(res.Header.Get("Expires"))
	if res.Header.Get("Cache-Control") != "max-age=20" || err != nil || time.Until(exp) > 21*time.Second || time.Until(exp) < 18*time.Second {
		t.Errorf("home headers = %v", res.Header)
	}
}

func TestBadQueries(t *testing.T) {
	c, _ := newAPI(t, nil, items())
	for _, q := range []string{
		`where={bad`,
		`where={"$where":"1"}`,
		`where={"name":{"$regex":"("}}`,
		`sort=[(]`,
		`projection={bad`,
	} {
		if res := c.Do("GET", "/items?"+q, nil); res.Status != http.StatusBadRequest {
			t.Errorf("%s = %d %s", q, res.Status, res.Raw)
		}
	}
}

func TestBadBodies(t *testing.T) {
	c, _ := newAPI(t, nil, items())
	for _, body := range []string{``, `[]`, `"x"`, `[1]`, `{bad`} {
		if res := c.Do("POST", "/items", body); res.Status != http.StatusBadRequest {
			t.Errorf("%q = %d", body, res.Status)
		}
	}
	created := c.Do("POST", "/items", map[string]any{"name": "a"})
	if res := c.Do("PATCH", "/items/"+created.String("_id"), `[{}]`, "If-Match", created.String("_etag")); res.Status != http.StatusBadRequest {
		t.Errorf("PATCH with a list = %d", res.Status)
	}
}

func TestBulkInsertionFailure(t *testing.T) {
	c, _ := newAPI(t, nil, items())
	res := c.Do("POST", "/items", []any{map[string]any{"name": "a"}, map[string]any{"name": 1}})
	body := res.Map()
	list := body["_items"].([]any)
	if res.Status != http.StatusUnprocessableEntity || list[0].(map[string]any)["_status"] != "OK" || list[1].(map[string]any)["_status"] != "ERR" {
		t.Errorf("body = %v", body)
	}
}

func TestConditionalGet(t *testing.T) {
	c, _ := newAPI(t, nil, items())
	created := c.Do("POST", "/items", map[string]any{"name": "a"})
	path := "/items/" + created.String("_id")

	got := c.Do("GET", path, nil)
	if got.Header.Get("ETag") != `"`+created.String("_etag")+`"` {
		t.Errorf("ETag = %q", got.Header.Get("ETag"))
	}
	res := c.Do("GET", path, nil, "If-None-Match", `W/"`+created.String("_etag")+`"`)
	if res.Status != http.StatusNotModified {
		t.Errorf("If-None-Match = %d", res.Status)
	}
	// the 304 repeats the ETag (prdreplay 14-if-none-match-304)
	if res.Header.Get("ETag") != `"`+created.String("_etag")+`"` || res.Raw != "" {
		t.Errorf("304 ETag = %q, body %q", res.Header.Get("ETag"), res.Raw)
	}
	future := time.Now().Add(time.Hour).UTC().Format(http.TimeFormat)
	if res := c.Do("GET", path, nil, "If-Modified-Since", future); res.Status != http.StatusNotModified {
		t.Errorf("If-Modified-Since = %d", res.Status)
	}
	if res := c.Do("HEAD", path, nil); res.Status != http.StatusOK {
		t.Errorf("HEAD = %d", res.Status)
	}
}

func TestHardDelete(t *testing.T) {
	c, svc := newAPI(t, nil, items())
	created := c.Do("POST", "/items", map[string]any{"name": "a"})
	if res := c.Do("DELETE", "/items/"+created.String("_id"), nil, "If-Match", "x"); res.Status != http.StatusPreconditionFailed {
		t.Errorf("delete wrong etag = %d", res.Status)
	}
	if res := c.Do("DELETE", "/items/"+created.String("_id"), nil, "If-Match", created.String("_etag")); res.Status != http.StatusNoContent {
		t.Fatalf("delete = %d", res.Status)
	}
	if n, _ := svc.Resource().Store.(mongodb.Store).Collection.CountDocuments(context.Background(), bson.M{}); n != 0 {
		t.Errorf("document still stored")
	}
}

// Custom endpoints (publish, schedule) use the Service, RequireAuth,
// ReadBody, ItemBody and WriteError.
func TestCustomEndpointHelpers(t *testing.T) {
	keys := testkit.NewKeys(t)
	db := testkit.Mongo(t)
	api := evecompat.New(evecompat.Options{Auth: keys.Authenticator(t)})
	res := items()
	res.Store = mongodb.NewStore(db.Collection("items"))
	res.PublicMethods = evecompat.PublicGET
	api.Register(res)
	svc := api.Service("items")
	if svc == nil || api.ResourceURLs()[0] != "items" {
		t.Fatal("service not registered")
	}
	api.Handle("POST /items/{id}/rename", api.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := evecompat.ReadBody(r)
		if err != nil {
			evecompat.WriteError(w, r, err)
			return
		}
		oid, _ := bson.ObjectIDFromHex(r.PathValue("id"))
		doc, err := svc.Patch(r.Context(), bson.M{"_id": oid}, evecompat.Doc{"name": body["name"]}, evecompat.WriteOptions{SkipConcurrency: true})
		if err != nil {
			evecompat.WriteError(w, r, err)
			return
		}
		evecompat.WriteJSON(w, http.StatusOK, svc.ItemBody(doc))
	})))
	c := testkit.NewClient(t, api)
	token := "Token " + keys.Token(t, "fulano")

	created := c.Do("POST", "/items", map[string]any{"name": "a"}, "Authorization", token)
	id := created.String("_id")
	if res := c.Do("POST", "/items/"+id+"/rename", map[string]any{"name": "b"}); res.Status != http.StatusForbidden {
		t.Errorf("without token = %d", res.Status)
	}
	res2 := c.Do("POST", "/items/"+id+"/rename", map[string]any{"name": "b"}, "Authorization", token)
	if res2.Status != http.StatusOK || res2.String("name") != "b" || !strings.HasSuffix(res2.Map()["_links"].(map[string]any)["self"].(map[string]any)["href"].(string), id) {
		t.Errorf("rename = %d %s", res2.Status, res2.Raw)
	}
	if res := c.Do("POST", "/items/"+id+"/rename", `{bad`, "Authorization", token); res.Status != http.StatusBadRequest {
		t.Errorf("bad body = %d", res.Status)
	}
	if res := c.Do("POST", "/items/"+id+"/rename", map[string]any{"name": 1}, "Authorization", token); res.Status != http.StatusUnprocessableEntity {
		t.Errorf("invalid = %d", res.Status)
	}
	missing := bson.NewObjectID().Hex()
	if res := c.Do("POST", "/items/"+missing+"/rename", map[string]any{"name": "x"}, "Authorization", token); res.Status != http.StatusNotFound {
		t.Errorf("missing = %d", res.Status)
	}
}

func TestErrorTypes(t *testing.T) {
	if (&evecompat.Error{Status: 404}).Error() != "Not Found" || evecompat.Abort(400, "x").Error() != "x" {
		t.Error("Error()")
	}
	if (&evecompat.ValidationError{}).Error() == "" || (&evecompat.InsertError{}).Error() == "" || (&evecompat.PreconditionFailedError{}).Error() == "" {
		t.Error("empty messages")
	}
	if (evecompat.ErrDeniedOperator{Operator: "$where"}).Error() == "" {
		t.Error("denied operator message")
	}
}

// D-27: reads have a time limit. When it runs out the API answers 503
// instead of holding the request (in production, unindexed queries held
// articles-api workers for more than 20 s).
func TestQueryTimeout(t *testing.T) {
	db := testkit.Mongo(t)
	res := items()
	res.Store = mongodb.NewStore(db.Collection("items"))
	api := evecompat.New(evecompat.Options{QueryTimeout: time.Nanosecond})
	api.Register(res)
	c := testkit.NewClient(t, api)
	for _, path := range []string{"/items", "/items/" + bson.NewObjectID().Hex()} {
		got := c.Do("GET", path, nil)
		if got.Status != http.StatusServiceUnavailable || got.Map()["_status"] != "ERR" {
			t.Errorf("%s = %d %s", path, got.Status, got.Raw)
		}
	}
}

// Eve's Allow for an item lists the resource methods too, when the item has
// more than GET (the contracts compare it with the real Eve).
func TestItemOptions(t *testing.T) {
	c, _ := newAPI(t, nil, items())
	id := c.Do("POST", "/items", map[string]any{"name": "a"}).String("_id")
	if got := c.Do("OPTIONS", "/items/"+id, nil).Header.Get("Allow"); got != "GET, PATCH, PUT, DELETE, POST, OPTIONS, HEAD" {
		t.Errorf("Allow = %q", got)
	}

	res := items()
	res.ItemMethods = []string{http.MethodGet}
	c, _ = newAPI(t, nil, res)
	id = c.Do("POST", "/items", map[string]any{"name": "a"}).String("_id")
	if got := c.Do("OPTIONS", "/items/"+id, nil).Header.Get("Allow"); got != "GET, OPTIONS, HEAD" {
		t.Errorf("GET only: Allow = %q", got)
	}
	if got := c.Do("PATCH", "/items/"+id, map[string]any{"name": "b"}); got.Status != http.StatusMethodNotAllowed {
		t.Errorf("PATCH not allowed = %d", got.Status)
	}
}

// A body that is not JSON, or a list where only an object is accepted, is
// Eve's 400 without a message.
func TestInvalidBodies(t *testing.T) {
	c, _ := newAPI(t, nil, items())
	created := c.Do("POST", "/items", map[string]any{"name": "a"})
	id, etag := created.String("_id"), created.String("_etag")
	for _, tc := range []struct{ method, path, body string }{
		{"POST", "/items", `{bad`},
		{"POST", "/items", `[]`},
		{"POST", "/items", `"texto"`},
		{"PATCH", "/items/" + id, `[{"name":"b"}]`},
		{"PUT", "/items/" + id, `{bad`},
	} {
		res := c.Do(tc.method, tc.path, tc.body, "If-Match", etag)
		if res.Status != http.StatusBadRequest {
			t.Errorf("%s %s %s = %d %s", tc.method, tc.path, tc.body, res.Status, res.Raw)
		}
	}
}
