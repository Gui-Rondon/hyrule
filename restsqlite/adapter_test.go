package restsqlite

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gui-Rondon/hyrule/platform/sqlite"
	"github.com/Gui-Rondon/hyrule/rest"
	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestHTTPPersistenceAndConcurrency(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "test.db")
	db, err := sqlite.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	res := &rest.Resource{Name: "items", Schema: rest.Schema{"name": {Type: rest.TypeString, Required: true, Unique: true}, "price": {Type: rest.TypeNumber}, "detail": {Type: rest.TypeDict, AllowUnknown: true}}, ResourceMethods: []string{"GET", "POST"}, ItemMethods: []string{"GET", "PATCH", "PUT", "DELETE"}, RequireMergePatchContentType: true}
	a, err := NewAdapter(ctx, db, "items", res)
	if err != nil {
		t.Fatal(err)
	}
	res.Adapter = a
	api := rest.New(rest.Options{Transactor: sqlite.Transactor{DB: db}})
	api.Register(res)
	request := func(method, path, body, etag string, status int) map[string]any {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/merge-patch+json")
		if etag != "" {
			r.Header.Set("If-Match", etag)
		}
		w := httptest.NewRecorder()
		api.ServeHTTP(w, r)
		if w.Code != status {
			t.Fatalf("%s %s: %d want %d: %s", method, path, w.Code, status, w.Body)
		}
		var out map[string]any
		if w.Body.Len() > 0 {
			_ = json.Unmarshal(w.Body.Bytes(), &out)
		}
		return out
	}
	request("POST", "/items", `{}`, "", 422)
	doc := request("POST", "/items", `{"name":"sword","price":10,"detail":{"old":1,"keep":2}}`, "", 201)
	id := doc["_id"].(string)
	etag := doc["_etag"].(string)
	request("POST", "/items", `{"name":"sword"}`, "", 422)
	request("PATCH", "/items/"+id, `{"price":12}`, "", 428)
	request("PATCH", "/items/"+id, `{"price":12}`, `"stale"`, 412)
	updated := request("PATCH", "/items/"+id, `{"detail":{"old":null,"new":3}}`, `"`+etag+`"`, 200)
	detail := request("GET", "/items/"+id, "", "", 200)["detail"].(map[string]any)
	if _, ok := detail["old"]; ok || detail["keep"] != float64(2) {
		t.Fatalf("merge: %#v", detail)
	}
	request("PATCH", "/items/"+id, `{"name":null}`, `"`+updated["_etag"].(string)+`"`, 422)
	request("PATCH", "/items/"+id, `{"price":12}`, `"`+etag+`"`, 412)
	request("POST", "/items", `{"name":"shield","price":20}`, "", 201)
	query := url.Values{"where": {`{"price":{"$gte":10}}`}, "sort": {"-price,name"}, "projection": {`{"name":1}`}, "max_results": {"1"}}
	list := request("GET", "/items?"+query.Encode(), "", "", 200)
	items := list["_items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["name"] != "shield" {
		t.Fatalf("list: %#v", list)
	}
	if _, ok := items[0].(map[string]any)["price"]; ok {
		t.Fatal("projection leaked price")
	}
	request("GET", "/items?where="+url.QueryEscape(`{"price":{"$regex":".*"}}`), "", "", 400)
	request("GET", "/api/openapi.json", "", "", 200)
	request("GET", "/missing", "", "", 404)
	request("PUT", "/items", "", "", 405)
	replaced := request("PUT", "/items/"+id, `{"name":"bow","price":30}`, `"`+updated["_etag"].(string)+`"`, 200)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db2, err := sqlite.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()
	reopened, err := NewAdapter(ctx, db2, "items", res)
	if err != nil {
		t.Fatal(err)
	}
	f, _ := reopened.IDFilter(id)
	saved, err := reopened.FindOne(ctx, f, nil)
	if err != nil || saved["name"] != "bow" || saved["_etag"] != replaced["_etag"] {
		t.Fatalf("persisted: %#v %v", saved, err)
	}
	n, err := reopened.DeleteOne(ctx, f)
	if err != nil || n != 1 {
		t.Fatalf("delete %d %v", n, err)
	}
	if _, err := reopened.FindOne(ctx, f, nil); !reopened.IsNotFound(err) {
		t.Fatalf("not found: %v", err)
	}
}
func TestAtomicBatchCASAndQueries(t *testing.T) {
	ctx := context.Background()
	db, err := sqlite.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	res := &rest.Resource{Schema: rest.Schema{"name": {Type: rest.TypeString, Unique: true}, "when": {Type: rest.TypeDatetime}}}
	seam, err := NewAdapter(ctx, db, "items", res)
	if err != nil {
		t.Fatal(err)
	}
	a := seam.(*adapter)
	makeDoc := func(id, name string) rest.Doc {
		return rest.Doc{"_id": id, "name": name, "_created": time.Now().UTC(), "_updated": time.Now().UTC(), "when": time.Now().UTC()}
	}
	if err := a.InsertMany(ctx, []rest.Doc{makeDoc("a", "same"), makeDoc("b", "same")}); !a.IsDuplicateKey(err) {
		t.Fatalf("unique: %v", err)
	}
	if n, err := a.CountDocuments(ctx, nil); err != nil || n != 0 {
		t.Fatalf("batch rollback %d %v", n, err)
	}
	d := makeDoc("a", "one")
	if err := a.InsertMany(ctx, []rest.Doc{d}); err != nil {
		t.Fatal(err)
	}
	lookup, _ := a.WriteLookup(d)
	next := makeDoc("a", "two")
	if n, err := a.UpdateOne(ctx, lookup, next, nil); err != nil || n != 1 || next["_etag"] != "2" {
		t.Fatalf("CAS %d %v %#v", n, err, next)
	}
	if n, err := a.ReplaceOne(ctx, lookup, d); err != nil || n != 0 {
		t.Fatalf("stale CAS %d %v", n, err)
	}
	for _, raw := range []string{`{}`, `{"$and":[{"name":"two"},{"_etag":"2"}]}`, `{"name":{"$ne":"one"}}`} {
		f, err := a.ParseWhere(raw, res)
		if err != nil {
			t.Fatal(err)
		}
		f, err = a.CombineFilter(res, f, false)
		if err != nil {
			t.Fatal(err)
		}
		if n, err := a.CountDocuments(ctx, f); err != nil || n != 1 {
			t.Fatalf("%s: %d %v", raw, n, err)
		}
	}
	f, _ := a.EqualsFilter("name", "two", "a")
	if n, err := a.CountDocuments(ctx, f); err != nil || n != 0 {
		t.Fatalf("exclude: %d %v", n, err)
	}
	f, _ = a.EqualsFilter("name", "two", nil)
	if n, err := a.CountDocuments(ctx, f); err != nil || n != 1 {
		t.Fatalf("equals: %d %v", n, err)
	}
	doc, err := a.FindOne(ctx, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := doc["when"].(time.Time); !ok {
		t.Fatal("datetime not hydrated")
	}
	id := a.NewID()
	if decoded, ok := a.LookupByID(a.IDString(id)); !ok || a.LookupID(decoded) != id {
		t.Fatal("id codec")
	}
	if _, ok := a.LookupByID("bad"); ok {
		t.Fatal("invalid UUID")
	}
	if !a.ValidatesMergedDocument() || !a.ClassifiesWriteTimeouts() || a.RetryPolicy().MaxAttempts != 0 || !a.IsTimeout(context.DeadlineExceeded) || a.IsTimeout(nil) || a.IsDuplicateKey(nil) || a.IsNotFound(nil) || a.IsClientError(nil) {
		t.Fatal("classification")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := a.Find(canceled, nil, nil, nil, 0, 0); err == nil {
		t.Fatal("canceled read")
	}
	if _, err := a.ReplaceOne(ctx, nil, rest.Doc{"bad": make(chan int)}); err == nil {
		t.Fatal("unserializable update")
	}
	if err := a.InsertMany(ctx, []rest.Doc{{"bad": make(chan int)}}); err == nil {
		t.Fatal("unserializable insert")
	}
	if _, err := a.Find(ctx, 1, nil, nil, 0, 0); err == nil {
		t.Fatal("invalid filter")
	}
	if _, err := a.Find(ctx, nil, 1, nil, 0, 0); err == nil {
		t.Fatal("invalid sort")
	}
	if _, err := a.Find(ctx, nil, nil, 1, 0, 0); err == nil {
		t.Fatal("invalid projection")
	}
	if _, err := a.CountDocuments(ctx, 1); err == nil {
		t.Fatal("invalid count filter")
	}
	if _, err := a.FindOne(ctx, 1, nil); err == nil {
		t.Fatal("invalid item filter")
	}
	if _, err := a.ReplaceOne(ctx, 1, d); err == nil {
		t.Fatal("invalid update filter")
	}
	if _, err := a.DeleteOne(ctx, 1); err == nil {
		t.Fatal("invalid delete filter")
	}
	if _, err := a.Find(ctx, nil, []sortTerm{{"x.y", false}}, nil, 0, 0); err == nil {
		t.Fatal("nested sort")
	}
	// Round-tripping an integer larger than float64's exact range must not corrupt it.
	exact := rest.Doc{"_id": "exact", "large": int64(9007199254740993)}
	if err := a.InsertMany(ctx, []rest.Doc{exact}); err != nil {
		t.Fatal(err)
	}
	exactFilter, _ := a.IDFilter("exact")
	stored, err := a.FindOne(ctx, exactFilter, nil)
	if err != nil || stored["large"] != json.Number("9007199254740993") {
		t.Fatalf("integer precision: %#v %v", stored, err)
	}

	// Infrastructure errors must not become client errors or successful writes.
	db.Close()
	if err := a.InsertMany(ctx, []rest.Doc{d}); err == nil {
		t.Fatal("closed insert")
	}
	if _, err := a.ReplaceOne(ctx, nil, d); err == nil {
		t.Fatal("closed update")
	}
	if _, err := a.DeleteOne(ctx, nil); err == nil {
		t.Fatal("closed delete")
	}
}
func TestQueryValidationAndProjection(t *testing.T) {
	a := &adapter{}
	res := &rest.Resource{}
	for _, raw := range []string{`null`, `[]`, `{`, `{"$or":[]}`, `{"$and":[]}`, `{"$and":[1]}`, `{"a.b":1}`, `{"name":{}}`, `{"name":{"$eq":[]}}`, `{"name":{"$in":[1]}}`} {
		if _, err := a.ParseWhere(raw, res); err == nil || !a.IsClientError(err) {
			t.Fatalf("accepted %s: %v", raw, err)
		} else if !strings.Contains(err.Error(), "restsqlite") {
			t.Fatal(err)
		}
	}
	if _, err := a.CombineFilter(&rest.Resource{SoftDelete: true}, nil, false); err == nil {
		t.Fatal("soft delete")
	}
	if _, err := a.CombineFilter(res, 1, false); err == nil {
		t.Fatal("bad filter")
	}
	if _, err := a.CombineFilter(&rest.Resource{Datasource: rest.Datasource{Filter: bson.M{"$or": 1}}}, nil, false); err == nil {
		t.Fatal("bad base filter")
	}
	for _, raw := range []string{"", "-name,_id"} {
		if _, err := a.ParseSort(raw); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := a.ParseSort("x.y"); err == nil {
		t.Fatal("nested sort accepted")
	}
	res.Datasource.DefaultSort = bson.D{{Key: "name", Value: -1}}
	if len(a.CombineSort(res, nil).([]sortTerm)) != 1 {
		t.Fatal("default sort")
	}
	if len(a.CombineSort(res, []sortTerm{{"_id", false}}).([]sortTerm)) != 1 {
		t.Fatal("client sort")
	}
	for _, raw := range []string{`null`, `{`, `{"a.b":1}`, `{"name":2}`, `{"name":1,"price":0}`, `{"name":[]}`} {
		if _, err := a.ParseProjection(raw); err == nil {
			t.Fatalf("accepted projection %s", raw)
		}
	}
	for _, raw := range []string{`{"name":1}`, `{"name":0}`, `{"name":true,"_id":false}`} {
		if _, err := a.ParseProjection(raw); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := a.CombineProjection(res, 1); err == nil {
		t.Fatal("bad projection type")
	}
	p, _ := a.CombineProjection(res, projection{"name": true})
	if got := project(rest.Doc{"name": "a", "price": 1, "_id": "id"}, p.(projection)); len(got) != 2 {
		t.Fatal(got)
	}
	res.Datasource.Projection = bson.M{"name": 1, "price": 1, "_id": 1, "secret": 0}
	p, _ = a.CombineProjection(res, projection{"name": true})
	got := project(rest.Doc{"name": "a", "price": 1, "secret": "hidden", "_id": "id"}, p.(projection))
	if len(got) != 2 {
		t.Fatal(got)
	}
	p, _ = a.CombineProjection(res, nil)
	if len(p.(projection)) != 4 {
		t.Fatal(p)
	}
	if len(project(rest.Doc{"name": "a", "price": 1}, projection{"name": false})) != 1 {
		t.Fatal("exclude projection")
	}
	if len(project(rest.Doc{"name": "a"}, nil)) != 1 {
		t.Fatal("empty projection")
	}
	original := rest.Doc{"_id": "a", "obj": map[string]any{"a": 1}, "arr": []any{1}}
	merged, err := a.MergePatch(original, rest.Doc{"obj": map[string]any{"b": 2}, "arr": []any{2}})
	if err != nil {
		t.Fatal(err)
	}
	merged["obj"].(map[string]any)["a"] = 3
	if original["obj"].(map[string]any)["a"] != 1 {
		t.Fatal("aliased object")
	}
}
