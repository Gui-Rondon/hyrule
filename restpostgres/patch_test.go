package restpostgres

import (
	"context"
	"reflect"
	"testing"

	"github.com/google/uuid"

	"github.com/Gui-Rondon/hyrule/rest"
)

func TestWriteLookupRoundTrip(t *testing.T) {
	a := &postgresAdapter{}
	id := uuid.New()
	original := rest.Doc{"_id": id, "_etag": "3"}
	lookup, err := a.WriteLookup(original)
	if err != nil {
		t.Fatal(err)
	}
	pl, ok := lookup.(postgresLookup)
	if !ok {
		t.Fatalf("WriteLookup = %T, want postgresLookup", lookup)
	}
	if pl.ID != id || pl.Version != 3 {
		t.Fatalf("WriteLookup = %+v, want ID=%v Version=3", pl, id)
	}
}

func TestWriteLookupRejectsInvalidEtag(t *testing.T) {
	a := &postgresAdapter{}
	cases := []rest.Doc{
		{"_id": uuid.New(), "_etag": "not-a-number"},
		{"_id": uuid.New()}, // missing _etag entirely
	}
	for i, doc := range cases {
		if _, err := a.WriteLookup(doc); err == nil {
			t.Errorf("case %d: expected an error for a malformed/missing _etag", i)
		}
	}
}

func TestMergePatchReplacesScalarAndDeletesNull(t *testing.T) {
	a := &postgresAdapter{}
	original := rest.Doc{"_id": uuid.New(), "_etag": "1", "name": "a", "status": "draft"}
	changes := rest.Doc{"name": "b", "status": nil}
	merged, err := a.MergePatch(original, changes)
	if err != nil {
		t.Fatal(err)
	}
	if merged["name"] != "b" {
		t.Fatalf("merged[name] = %v, want b", merged["name"])
	}
	if _, ok := merged["status"]; ok {
		t.Fatalf("merged[status] must be deleted (null patch value): %#v", merged)
	}
	if merged["_id"] != original["_id"] || merged["_etag"] != "1" {
		t.Fatalf("merged meta fields changed: %#v", merged)
	}
}

func TestMergePatchReplacesArrayWholesale(t *testing.T) {
	a := &postgresAdapter{}
	original := rest.Doc{"tags": []any{"a", "b", "c"}}
	changes := rest.Doc{"tags": []any{"x"}}
	merged, err := a.MergePatch(original, changes)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(merged["tags"], []any{"x"}) {
		t.Fatalf("merged[tags] = %#v, want [\"x\"] (arrays replace wholesale, never merge)", merged["tags"])
	}
}

func TestMergePatchMergesNestedObjectRecursively(t *testing.T) {
	a := &postgresAdapter{}
	original := rest.Doc{"meta": map[string]any{"author": "link", "views": 5.0}}
	changes := rest.Doc{"meta": map[string]any{"author": "zelda"}}
	merged, err := a.MergePatch(original, changes)
	if err != nil {
		t.Fatal(err)
	}
	meta, ok := merged["meta"].(map[string]any)
	if !ok {
		t.Fatalf("merged[meta] = %T, want map[string]any", merged["meta"])
	}
	if meta["author"] != "zelda" {
		t.Fatalf("meta[author] = %v, want zelda (patched)", meta["author"])
	}
	if meta["views"] != 5.0 {
		t.Fatalf("meta[views] = %v, want 5.0 (untouched, recursive merge)", meta["views"])
	}
}

func TestMergePatchObjectValueAgainstMissingOriginalField(t *testing.T) {
	// RFC 7396: when the target has no value at that key (or it isn't
	// an object), the patch's object value is used as-is.
	a := &postgresAdapter{}
	original := rest.Doc{}
	changes := rest.Doc{"meta": map[string]any{"author": "link"}}
	merged, err := a.MergePatch(original, changes)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(merged["meta"], map[string]any{"author": "link"}) {
		t.Fatalf("merged[meta] = %#v", merged["meta"])
	}
}

func TestMergePatchIgnoresMetaFieldsInChanges(t *testing.T) {
	// A client's PATCH body can't rewrite meta fields through the
	// merge -- the result always keeps original's.
	a := &postgresAdapter{}
	originalID := uuid.New()
	original := rest.Doc{"_id": originalID, "_etag": "5", "name": "a"}
	changes := rest.Doc{"_id": uuid.New(), "_etag": "999", "name": "b"}
	merged, err := a.MergePatch(original, changes)
	if err != nil {
		t.Fatal(err)
	}
	if merged["_id"] != originalID || merged["_etag"] != "5" {
		t.Fatalf("merged meta fields = _id=%v _etag=%v, want original's untouched", merged["_id"], merged["_etag"])
	}
}

func TestEqualsFilterIntegration(t *testing.T) {
	pool := testPool(t)
	a := newTestAdapter(t, pool, "patch_equalsfilter_test", nil)
	ctx := context.Background()

	doc1 := newDoc(a, map[string]any{"slug": "hello"})
	doc2 := newDoc(a, map[string]any{"slug": "world"})
	if err := a.InsertMany(ctx, []rest.Doc{doc1, doc2}); err != nil {
		t.Fatal(err)
	}

	filter, err := a.EqualsFilter("slug", "hello", nil)
	if err != nil {
		t.Fatal(err)
	}
	n, err := a.CountDocuments(ctx, filter)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("count without exclusion = %d, want 1", n)
	}

	// Excluding doc1 itself must bring the count to zero -- this is
	// exactly checkUnique's "does another document have this value"
	// question, not "does any document, including myself".
	id1 := doc1["_id"].(uuid.UUID)
	filterExcludingSelf, err := a.EqualsFilter("slug", "hello", id1)
	if err != nil {
		t.Fatal(err)
	}
	n, err = a.CountDocuments(ctx, filterExcludingSelf)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("count excluding self = %d, want 0", n)
	}
}

func TestEqualsFilterRejectsNonUUIDExcludeID(t *testing.T) {
	a := &postgresAdapter{}
	if _, err := a.EqualsFilter("slug", "x", "not-a-uuid"); err == nil {
		t.Fatal("expected an error for a non-uuid.UUID excludeID")
	}
}

// TestUniqueIndexIsTheRealGuarantee proves EqualsFilter is only the
// friendly pre-check -- a real race still relies on the unique index
// (postgres-rfc.md §5), surfacing as IsDuplicateKey (23505), never a
// silent duplicate.
func TestUniqueIndexIsTheRealGuarantee(t *testing.T) {
	pool := testPool(t)
	a := newTestAdapter(t, pool, "patch_unique_index_test", nil)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `CREATE UNIQUE INDEX ON patch_unique_index_test ((doc->'slug')) WHERE doc ? 'slug'`); err != nil {
		t.Fatal(err)
	}

	doc1 := newDoc(a, map[string]any{"slug": "hello"})
	if err := a.InsertMany(ctx, []rest.Doc{doc1}); err != nil {
		t.Fatal(err)
	}
	doc2 := newDoc(a, map[string]any{"slug": "hello"})
	err := a.InsertMany(ctx, []rest.Doc{doc2})
	if err == nil {
		t.Fatal("expected a unique violation inserting a second document with the same slug")
	}
	if !a.IsDuplicateKey(err) {
		t.Fatalf("err = %v, want IsDuplicateKey", err)
	}
}

func TestDeepCloneUnaffectedByOriginalMutation(t *testing.T) {
	// Not deepCloneDoc itself (that's rest/service.go's, Fase 4) --
	// this just documents that MergePatch's result shares no mutable
	// structure with original's nested values it didn't touch, so a
	// later mutation of merged doesn't corrupt original.
	a := &postgresAdapter{}
	original := rest.Doc{"meta": map[string]any{"author": "link"}}
	changes := rest.Doc{}
	merged, err := a.MergePatch(original, changes)
	if err != nil {
		t.Fatal(err)
	}
	merged["meta"].(map[string]any)["author"] = "zelda"
	if original["meta"].(map[string]any)["author"] != "link" {
		t.Fatal("mutating merged's nested map corrupted original -- MergePatch must copy, not alias")
	}
}
