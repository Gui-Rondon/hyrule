package restpostgres

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Gui-Rondon/hyrule/platform/postgres"
	"github.com/Gui-Rondon/hyrule/rest"
)

// newDoc builds a rest.Doc the way Service.Insert would have it, right
// before calling Adapter.InsertMany: _id/_created/_updated already
// set, no _etag yet (InsertMany sets that itself).
func newDoc(a *postgresAdapter, fields map[string]any) rest.Doc {
	now := time.Now().UTC().Truncate(time.Microsecond)
	doc := rest.Doc{"_id": a.NewID(), "_created": now, "_updated": now}
	for k, v := range fields {
		doc[k] = v
	}
	return doc
}

func TestInsertManyAndFindOne(t *testing.T) {
	pool := testPool(t)
	a := newTestAdapter(t, pool, "crud_insert_findone_test", nil)
	ctx := context.Background()

	doc := newDoc(a, map[string]any{"name": "a"})
	if err := a.InsertMany(ctx, []rest.Doc{doc}); err != nil {
		t.Fatal(err)
	}
	if doc["_etag"] != "1" {
		t.Fatalf("doc[_etag] after insert = %v, want \"1\"", doc["_etag"])
	}

	got, err := a.FindOne(ctx, doc["_id"], nil)
	if err != nil {
		t.Fatal(err)
	}
	if got["name"] != "a" {
		t.Fatalf("got[name] = %v, want a", got["name"])
	}
	if got["_etag"] != "1" {
		t.Fatalf("got[_etag] = %v, want \"1\"", got["_etag"])
	}
	if got["_created"] != got["_updated"] {
		t.Fatalf("_created (%v) != _updated (%v) on a fresh insert", got["_created"], got["_updated"])
	}
	if _, ok := got["_id"].(uuid.UUID); !ok {
		t.Fatalf("got[_id] = %T, want uuid.UUID", got["_id"])
	}
	if _, ok := got["_deleted"]; ok {
		t.Fatalf("got[_deleted] = %v, want absent (this adapter has SoftDelete off)", got["_deleted"])
	}
}

func TestSoftDeleteExposesDeletedField(t *testing.T) {
	pool := testPool(t)
	table := "crud_softdelete_test"
	setupResourceTable(t, pool, table)
	a, err := NewAdapter(postgres.NewStore(pool, table), postgres.Transactor{Pool: pool}, Options{
		SoftDelete: true,
		Retry:      DefaultRetryOptions(),
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	doc := newDoc(a, map[string]any{"name": "a"})
	if err := a.InsertMany(ctx, []rest.Doc{doc}); err != nil {
		t.Fatal(err)
	}
	got, err := a.FindOne(ctx, doc["_id"], nil)
	if err != nil {
		t.Fatal(err)
	}
	if deleted, ok := got["_deleted"].(bool); !ok || deleted {
		t.Fatalf("got[_deleted] = %#v, want false (present, SoftDelete is on)", got["_deleted"])
	}
}

func TestInsertManyBatchIsIndividuallyReadable(t *testing.T) {
	pool := testPool(t)
	a := newTestAdapter(t, pool, "crud_insert_batch_test", nil)
	ctx := context.Background()

	docs := make([]rest.Doc, 5)
	for i := range docs {
		docs[i] = newDoc(a, map[string]any{"name": "n", "i": i})
	}
	if err := a.InsertMany(ctx, docs); err != nil {
		t.Fatal(err)
	}
	for i, doc := range docs {
		got, err := a.FindOne(ctx, doc["_id"], nil)
		if err != nil {
			t.Fatalf("doc %d: %v", i, err)
		}
		// json.Unmarshal decodes a JSON number into float64 -- confirm
		// without assuming a specific numeric Go type.
		if n, ok := got["i"].(float64); !ok || int(n) != i {
			t.Fatalf("doc %d: got[i] = %v (%T), want %d", i, got["i"], got["i"], i)
		}
		if got["_etag"] != "1" {
			t.Fatalf("doc %d: _etag = %v, want \"1\"", i, got["_etag"])
		}
	}
}

func TestInsertManyEmpty(t *testing.T) {
	pool := testPool(t)
	a := newTestAdapter(t, pool, "crud_insert_empty_test", nil)
	if err := a.InsertMany(context.Background(), nil); err != nil {
		t.Fatalf("InsertMany(nil) = %v, want nil", err)
	}
}

func TestInsertManyRequiresMetaFields(t *testing.T) {
	pool := testPool(t)
	a := newTestAdapter(t, pool, "crud_insert_missing_meta_test", nil)
	cases := []rest.Doc{
		{"_created": time.Now(), "_updated": time.Now()}, // missing _id
		{"_id": a.NewID(), "_updated": time.Now()},       // missing _created
		{"_id": a.NewID(), "_created": time.Now()},       // missing _updated
	}
	for i, doc := range cases {
		if err := a.InsertMany(context.Background(), []rest.Doc{doc}); err == nil {
			t.Errorf("case %d: expected an error for a doc missing a required meta field", i)
		}
	}
}

func TestFindOneNotFound(t *testing.T) {
	pool := testPool(t)
	a := newTestAdapter(t, pool, "crud_findone_notfound_test", nil)
	if _, err := a.FindOne(context.Background(), uuid.New(), nil); !a.IsNotFound(err) {
		t.Fatalf("FindOne(missing id) err = %v, want IsNotFound", err)
	}
}

func TestFindWithFilterSortSkipLimit(t *testing.T) {
	pool := testPool(t)
	a := newTestAdapter(t, pool, "crud_find_test", map[string]postgres.Kind{"rank": postgres.KindInteger})
	ctx := context.Background()

	for _, d := range []map[string]any{
		{"name": "a", "rank": 3, "status": "x"},
		{"name": "b", "rank": 1, "status": "x"},
		{"name": "c", "rank": 2, "status": "y"},
	} {
		doc := newDoc(a, d)
		if err := a.InsertMany(ctx, []rest.Doc{doc}); err != nil {
			t.Fatal(err)
		}
	}

	// Filter: status == "x" -> a, b.
	docs, err := a.Find(ctx, postgres.Query{"status": "x"}, nil, nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 2 {
		t.Fatalf("filtered docs = %d, want 2", len(docs))
	}

	// Sort by rank ascending, no filter.
	docs, err = a.Find(ctx, nil, []SortTerm{{Field: "rank"}}, nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 3 || docs[0]["name"] != "b" || docs[1]["name"] != "c" || docs[2]["name"] != "a" {
		t.Fatalf("sorted docs = %#v", docs)
	}

	// Pagination: skip 1, limit 1, ascending by rank -> the middle ("c").
	docs, err = a.Find(ctx, nil, []SortTerm{{Field: "rank"}}, nil, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 1 || docs[0]["name"] != "c" {
		t.Fatalf("paged docs = %#v", docs)
	}
}

func TestFindNoLimitIgnoresSkip(t *testing.T) {
	pool := testPool(t)
	a := newTestAdapter(t, pool, "crud_find_nolimit_test", nil)
	ctx := context.Background()
	for _, name := range []string{"a", "b"} {
		doc := newDoc(a, map[string]any{"name": name})
		if err := a.InsertMany(ctx, []rest.Doc{doc}); err != nil {
			t.Fatal(err)
		}
	}
	docs, err := a.Find(ctx, nil, nil, nil, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 2 {
		t.Fatalf("docs = %d, want both despite skip=10 (limit<=0 disables it)", len(docs))
	}
}

func TestFindAppliesProjection(t *testing.T) {
	pool := testPool(t)
	a := newTestAdapter(t, pool, "crud_find_projection_test", nil)
	ctx := context.Background()
	doc := newDoc(a, map[string]any{"name": "a", "secret": "s"})
	if err := a.InsertMany(ctx, []rest.Doc{doc}); err != nil {
		t.Fatal(err)
	}
	docs, err := a.Find(ctx, nil, nil, Projection{"name": true}, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 1 {
		t.Fatalf("docs = %d, want 1", len(docs))
	}
	if _, ok := docs[0]["secret"]; ok {
		t.Fatalf("projection leaked secret: %#v", docs[0])
	}
	if docs[0]["name"] != "a" {
		t.Fatalf("docs[0][name] = %v, want a", docs[0]["name"])
	}
	if _, ok := docs[0]["_id"]; !ok {
		t.Fatal("projection must always keep meta fields")
	}
}

func TestFindRejectsWrongSortType(t *testing.T) {
	pool := testPool(t)
	a := newTestAdapter(t, pool, "crud_find_badsort_test", nil)
	if _, err := a.Find(context.Background(), nil, "not a []SortTerm", nil, 0, 0); err == nil {
		t.Fatal("expected an error for a sort value that isn't a []SortTerm")
	}
}

func TestFindRejectsWrongProjectionType(t *testing.T) {
	pool := testPool(t)
	a := newTestAdapter(t, pool, "crud_find_badprojection_test", nil)
	if _, err := a.Find(context.Background(), nil, nil, "not a Projection", 0, 0); err == nil {
		t.Fatal("expected an error for a projection value that isn't a Projection")
	}
}

func TestFindOneRejectsWrongProjectionType(t *testing.T) {
	pool := testPool(t)
	a := newTestAdapter(t, pool, "crud_findone_badprojection_test", nil)
	ctx := context.Background()
	doc := newDoc(a, map[string]any{"name": "a"})
	if err := a.InsertMany(ctx, []rest.Doc{doc}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.FindOne(ctx, doc["_id"], "not a Projection"); err == nil {
		t.Fatal("expected an error for a projection value that isn't a Projection")
	}
}

func TestFindSortsByNestedPath(t *testing.T) {
	pool := testPool(t)
	a := newTestAdapter(t, pool, "crud_find_nestedsort_test", nil)
	ctx := context.Background()
	for _, name := range []string{"link", "zelda", "ganon"} {
		doc := newDoc(a, map[string]any{"meta": map[string]any{"author": name}})
		if err := a.InsertMany(ctx, []rest.Doc{doc}); err != nil {
			t.Fatal(err)
		}
	}
	docs, err := a.Find(ctx, nil, []SortTerm{{Field: "meta.author"}}, nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 3 {
		t.Fatalf("docs = %d, want 3", len(docs))
	}
	authorAt := func(i int) any { return docs[i]["meta"].(map[string]any)["author"] }
	if authorAt(0) != "ganon" || authorAt(1) != "link" || authorAt(2) != "zelda" {
		t.Fatalf("order = %v, %v, %v, want ganon, link, zelda (alphabetical)", authorAt(0), authorAt(1), authorAt(2))
	}
}

func TestCountDocuments(t *testing.T) {
	pool := testPool(t)
	a := newTestAdapter(t, pool, "crud_count_test", nil)
	ctx := context.Background()
	for _, status := range []string{"draft", "draft", "published"} {
		doc := newDoc(a, map[string]any{"status": status})
		if err := a.InsertMany(ctx, []rest.Doc{doc}); err != nil {
			t.Fatal(err)
		}
	}
	n, err := a.CountDocuments(ctx, postgres.Query{"status": "draft"})
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("count = %d, want 2", n)
	}
}

func TestUpdateOneRoundTrip(t *testing.T) {
	pool := testPool(t)
	a := newTestAdapter(t, pool, "crud_update_test", nil)
	ctx := context.Background()
	doc := newDoc(a, map[string]any{"name": "a"})
	if err := a.InsertMany(ctx, []rest.Doc{doc}); err != nil {
		t.Fatal(err)
	}
	original, err := a.FindOne(ctx, doc["_id"], nil)
	if err != nil {
		t.Fatal(err)
	}

	lookup, err := a.WriteLookup(original)
	if err != nil {
		t.Fatal(err)
	}
	updated := rest.Doc{"_id": original["_id"], "name": "b"}
	matched, err := a.UpdateOne(ctx, lookup, updated, nil)
	if err != nil {
		t.Fatal(err)
	}
	if matched != 1 {
		t.Fatalf("matched = %d, want 1", matched)
	}
	if updated["_etag"] != "2" {
		t.Fatalf("updated[_etag] = %v, want \"2\"", updated["_etag"])
	}

	after, err := a.FindOne(ctx, doc["_id"], nil)
	if err != nil {
		t.Fatal(err)
	}
	if after["name"] != "b" {
		t.Fatalf("after update, name = %v", after["name"])
	}
	if after["_etag"] != "2" {
		t.Fatalf("after update, _etag = %v, want \"2\"", after["_etag"])
	}
	if after["_updated"] == after["_created"] {
		t.Fatal("after update, _updated must differ from _created")
	}
}

func TestUpdateOneStaleCASConflict(t *testing.T) {
	pool := testPool(t)
	a := newTestAdapter(t, pool, "crud_update_cas_test", nil)
	ctx := context.Background()
	doc := newDoc(a, map[string]any{"name": "a"})
	if err := a.InsertMany(ctx, []rest.Doc{doc}); err != nil {
		t.Fatal(err)
	}
	original, err := a.FindOne(ctx, doc["_id"], nil)
	if err != nil {
		t.Fatal(err)
	}
	staleLookup, err := a.WriteLookup(original)
	if err != nil {
		t.Fatal(err)
	}

	// A first write succeeds and advances the version...
	matched, err := a.UpdateOne(ctx, staleLookup, rest.Doc{"_id": original["_id"], "name": "b"}, nil)
	if err != nil || matched != 1 {
		t.Fatalf("first update: matched=%d err=%v", matched, err)
	}

	// ...so retrying with the now-stale lookup (still version 1) must
	// match zero rows, with no error -- the caller decides 404 vs 412.
	matched, err = a.UpdateOne(ctx, staleLookup, rest.Doc{"_id": original["_id"], "name": "c"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if matched != 0 {
		t.Fatalf("stale update: matched = %d, want 0", matched)
	}
}

func TestUpdateOneByBareIDIgnoresVersion(t *testing.T) {
	pool := testPool(t)
	a := newTestAdapter(t, pool, "crud_update_bareid_test", nil)
	ctx := context.Background()
	doc := newDoc(a, map[string]any{"name": "a"})
	if err := a.InsertMany(ctx, []rest.Doc{doc}); err != nil {
		t.Fatal(err)
	}
	id := doc["_id"].(uuid.UUID)
	updated := rest.Doc{"_id": id, "name": "b"}
	matched, err := a.UpdateOne(ctx, id, updated, nil)
	if err != nil {
		t.Fatal(err)
	}
	if matched != 1 {
		t.Fatalf("matched = %d, want 1", matched)
	}
}

func TestReplaceOne(t *testing.T) {
	pool := testPool(t)
	a := newTestAdapter(t, pool, "crud_replace_test", nil)
	ctx := context.Background()
	doc := newDoc(a, map[string]any{"name": "a", "status": "draft"})
	if err := a.InsertMany(ctx, []rest.Doc{doc}); err != nil {
		t.Fatal(err)
	}
	id := doc["_id"].(uuid.UUID)
	replacement := rest.Doc{"_id": id, "status": "published"} // "name" dropped: a full replace
	matched, err := a.ReplaceOne(ctx, id, replacement)
	if err != nil {
		t.Fatal(err)
	}
	if matched != 1 {
		t.Fatalf("matched = %d, want 1", matched)
	}
	after, err := a.FindOne(ctx, id, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := after["name"]; ok {
		t.Fatalf("after replace, name must be gone: %#v", after)
	}
	if after["status"] != "published" {
		t.Fatalf("after replace, status = %v", after["status"])
	}
}

func TestDeleteOne(t *testing.T) {
	pool := testPool(t)
	a := newTestAdapter(t, pool, "crud_delete_test", nil)
	ctx := context.Background()
	doc := newDoc(a, map[string]any{"name": "a"})
	if err := a.InsertMany(ctx, []rest.Doc{doc}); err != nil {
		t.Fatal(err)
	}
	id := doc["_id"].(uuid.UUID)
	deleted, err := a.DeleteOne(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 1 {
		t.Fatalf("deleted = %d, want 1", deleted)
	}
	if _, err := a.FindOne(ctx, id, nil); !a.IsNotFound(err) {
		t.Fatalf("FindOne after delete: err = %v, want IsNotFound", err)
	}
}

func TestDeleteOneNoMatch(t *testing.T) {
	pool := testPool(t)
	a := newTestAdapter(t, pool, "crud_delete_nomatch_test", nil)
	deleted, err := a.DeleteOne(context.Background(), uuid.New())
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 0 {
		t.Fatalf("deleted = %d, want 0", deleted)
	}
}

func TestLookupWhereRejectsBadInput(t *testing.T) {
	a := &postgresAdapter{}
	cases := []any{
		"not a uuid or postgresLookup",
		postgresLookup{ID: "not a uuid.UUID", Version: 1},
	}
	for i, lookup := range cases {
		if _, _, err := a.lookupWhere(lookup, 0); err == nil {
			t.Errorf("case %d: expected an error for lookup %#v", i, lookup)
		}
	}
}

func TestCompileFilterRejectsBadFilterType(t *testing.T) {
	a := &postgresAdapter{}
	if _, _, err := a.compileFilter("not a postgres.Query", 0); err == nil {
		t.Fatal("expected an error for a filter that isn't a postgres.Query/equalsFilter")
	}
}

func TestFindOneRejectsBadLookup(t *testing.T) {
	pool := testPool(t)
	a := newTestAdapter(t, pool, "crud_findone_badlookup_test", nil)
	if _, err := a.FindOne(context.Background(), "garbage", nil); err == nil {
		t.Fatal("expected an error for a lookup that isn't a uuid.UUID/postgresLookup")
	}
}

func TestCountDocumentsRejectsBadFilter(t *testing.T) {
	pool := testPool(t)
	a := newTestAdapter(t, pool, "crud_count_badfilter_test", nil)
	if _, err := a.CountDocuments(context.Background(), "garbage"); err == nil {
		t.Fatal("expected an error for a filter that isn't a postgres.Query")
	}
}

func TestDeleteOneRejectsBadLookup(t *testing.T) {
	pool := testPool(t)
	a := newTestAdapter(t, pool, "crud_delete_badlookup_test", nil)
	if _, err := a.DeleteOne(context.Background(), "garbage"); err == nil {
		t.Fatal("expected an error for a lookup that isn't a uuid.UUID/postgresLookup")
	}
}

// TestVersionOverflowIsInternalNeverClientOrDuplicate locks in
// postgres-rfc.md §7: version is bigint precisely so this is an
// internal failure (22003), never a silent wraparound, and it must
// never be classified as IsDuplicateKey/IsClientError/IsTimeout.
func TestVersionOverflowIsInternalNeverMisclassified(t *testing.T) {
	pool := testPool(t)
	a := newTestAdapter(t, pool, "crud_version_overflow_test", nil)
	ctx := context.Background()
	doc := newDoc(a, map[string]any{"name": "a"})
	if err := a.InsertMany(ctx, []rest.Doc{doc}); err != nil {
		t.Fatal(err)
	}
	id := doc["_id"].(uuid.UUID)
	if _, err := pool.Exec(ctx, "UPDATE crud_version_overflow_test SET version = 9223372036854775807 WHERE id = $1::uuid", id.String()); err != nil {
		t.Fatal(err)
	}

	lookup := postgresLookup{ID: id, Version: 9223372036854775807}
	_, err := a.UpdateOne(ctx, lookup, rest.Doc{"_id": id, "name": "b"}, nil)
	if err == nil {
		t.Fatal("expected an error: version+1 overflows bigint")
	}
	if a.IsDuplicateKey(err) || a.IsClientError(err) || a.IsTimeout(err) || a.IsNotFound(err) {
		t.Fatalf("version overflow misclassified: %v", err)
	}

	// The row must be untouched (the UPDATE statement failed outright,
	// no partial write).
	after, err := a.FindOne(ctx, id, nil)
	if err != nil {
		t.Fatal(err)
	}
	if after["name"] != "a" {
		t.Fatalf("after failed overflow update, name = %v, want unchanged \"a\"", after["name"])
	}
}
