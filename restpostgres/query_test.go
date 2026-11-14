package restpostgres

import (
	"context"
	"reflect"
	"testing"

	"github.com/Gui-Rondon/hyrule/platform/postgres"
	"github.com/Gui-Rondon/hyrule/rest"
)

func TestParseWhereDecodesJSON(t *testing.T) {
	a := &postgresAdapter{}
	got, err := a.ParseWhere(`{"status": {"$gt": "a"}}`, &rest.Resource{})
	if err != nil {
		t.Fatal(err)
	}
	want := postgres.Query{"status": map[string]any{"$gt": "a"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ParseWhere = %#v, want %#v", got, want)
	}
}

func TestParseWhereRejectsInvalidJSON(t *testing.T) {
	a := &postgresAdapter{}
	if _, err := a.ParseWhere(`not json`, &rest.Resource{}); err == nil {
		t.Fatal("expected an error for invalid JSON")
	}
}

// TestParseWhereKnownNumericGap locks in, rather than leaves
// undiscovered, the numeric-policy gap query.go's doc comment names:
// a plain json.Unmarshal decodes every number as float64, which
// checkKindValue (platform/postgres/query.go) rejects for
// KindInteger. Fixing this is Fase 0b's 🔴 decision, not this cut's.
func TestParseWhereKnownNumericGap(t *testing.T) {
	pool := testPool(t)
	table := "query_numeric_gap_test"
	a := newTestAdapter(t, pool, table, map[string]postgres.Kind{"views": postgres.KindInteger})

	where, err := a.ParseWhere(`{"views": {"$gt": 10}}`, &rest.Resource{})
	if err != nil {
		t.Fatal(err)
	}
	filter, err := a.CombineFilter(&rest.Resource{}, where, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.CountDocuments(context.Background(), filter); err == nil {
		t.Fatal("expected an error: float64 (from plain JSON decode) against a KindInteger field -- the known, named Fase 0b gap")
	}
}

func TestCombineFilterRejectsWrongType(t *testing.T) {
	a := &postgresAdapter{}
	if _, err := a.CombineFilter(&rest.Resource{}, "not a query", false); err == nil {
		t.Fatal("expected an error for a clientFilter that isn't a postgres.Query")
	}
}

func TestCombineFilterNilIsEmptyQuery(t *testing.T) {
	a := &postgresAdapter{}
	got, err := a.CombineFilter(&rest.Resource{}, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	want := combinedFilter{}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("CombineFilter(nil) = %#v, want %#v", got, want)
	}
}

func TestCombineFilterSoftDeleteIntegration(t *testing.T) {
	pool := testPool(t)
	table := "query_softdelete_test"
	a := newTestAdapter(t, pool, table, nil)
	// a.softDelete stays off deliberately: CombineFilter must read
	// soft-delete policy from res.SoftDelete (the Resource it's
	// called with), never from the adapter's own cached field -- the
	// exact divergence code review caught.
	res := &rest.Resource{SoftDelete: true}

	ctx := context.Background()
	if err := a.InsertMany(ctx, []rest.Doc{newDoc(a, map[string]any{"name": "visible"})}); err != nil {
		t.Fatal(err)
	}
	if err := a.InsertMany(ctx, []rest.Doc{newDoc(a, map[string]any{"name": "gone"})}); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "UPDATE "+table+" SET deleted = true WHERE doc->>'name' = 'gone'"); err != nil {
		t.Fatal(err)
	}

	visibleOnly, err := a.CombineFilter(res, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	docs, err := a.Find(ctx, visibleOnly, nil, nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 1 || docs[0]["name"] != "visible" {
		t.Fatalf("docs = %#v, want only the non-deleted row", docs)
	}

	everything, err := a.CombineFilter(res, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	docs, err = a.Find(ctx, everything, nil, nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 2 {
		t.Fatalf("docs (includeDeleted) = %#v, want both rows", docs)
	}
}

func TestParseSortRoundTrip(t *testing.T) {
	a := &postgresAdapter{}
	got, err := a.ParseSort("-name,slug")
	if err != nil {
		t.Fatal(err)
	}
	want := []SortTerm{{Field: "name", Desc: true}, {Field: "slug", Desc: false}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ParseSort = %#v, want %#v", got, want)
	}
}

func TestParseSortEmptyIsNoSort(t *testing.T) {
	a := &postgresAdapter{}
	got, err := a.ParseSort("")
	if err != nil || got != nil {
		t.Fatalf("ParseSort(\"\") = %#v, %v, want nil, nil", got, err)
	}
}

func TestParseSortRejectsEmptyFields(t *testing.T) {
	a := &postgresAdapter{}
	if _, err := a.ParseSort("  ,  "); err == nil {
		t.Fatal("expected an error: every field empty after trimming")
	}
}

// TestParseSortRejectsBareDash locks in code review's finding: a bare
// "-" (or "-,field") used to slip past the empty-field check (checked
// before stripping the "-" prefix, not after), producing a SortTerm
// with an empty Field instead of an error.
func TestParseSortRejectsBareDash(t *testing.T) {
	a := &postgresAdapter{}
	for _, raw := range []string{"-", "-,field", "field,-"} {
		if _, err := a.ParseSort(raw); err == nil {
			t.Errorf("ParseSort(%q): expected an error, got none", raw)
		}
	}
}

func TestCombineSortPassesThroughOrDefaultsToNoSort(t *testing.T) {
	// toSortTerms (sort.go) is what actually consumes CombineSort's
	// result -- asserted through it, not by comparing the any-typed
	// return against a bare nil (a nil []SortTerm boxed in an any is
	// never == nil).
	a := &postgresAdapter{}
	terms := []SortTerm{{Field: "name"}}
	if got := a.CombineSort(&rest.Resource{}, terms); !reflect.DeepEqual(got, terms) {
		t.Errorf("CombineSort(terms) = %#v, want %#v", got, terms)
	}
	for _, clientSort := range []any{nil, "not a []SortTerm"} {
		got, err := toSortTerms(a.CombineSort(&rest.Resource{}, clientSort))
		if err != nil || len(got) != 0 {
			t.Errorf("CombineSort(%#v) -> toSortTerms = %#v, %v, want no sort, no error", clientSort, got, err)
		}
	}
}

func TestParseProjectionDecodesTruthy(t *testing.T) {
	a := &postgresAdapter{}
	got, err := a.ParseProjection(`{"name": 1, "slug": 0, "flag": true, "off": false}`)
	if err != nil {
		t.Fatal(err)
	}
	want := Projection{"name": true, "slug": false, "flag": true, "off": false}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ParseProjection = %#v, want %#v", got, want)
	}
}

func TestParseProjectionRejectsInvalidJSON(t *testing.T) {
	a := &postgresAdapter{}
	if _, err := a.ParseProjection(`not json`); err == nil {
		t.Fatal("expected an error for invalid JSON")
	}
}

func TestCombineProjectionPassesThroughOrDefaultsToEverything(t *testing.T) {
	// toProjection (projection.go) is what actually consumes
	// CombineProjection's result -- asserted through it, same reasoning
	// as TestCombineSortPassesThroughOrDefaultsToNoSort.
	a := &postgresAdapter{}
	proj := Projection{"name": true}
	if got, err := a.CombineProjection(&rest.Resource{}, proj); err != nil || !reflect.DeepEqual(got, proj) {
		t.Errorf("CombineProjection(proj) = %#v, %v, want %#v, nil", got, err, proj)
	}
	combined, err := a.CombineProjection(&rest.Resource{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err := toProjection(combined)
	if err != nil || len(got) != 0 {
		t.Errorf("CombineProjection(nil) -> toProjection = %#v, %v, want no projection, no error", got, err)
	}
}

// TestPostgresAdapterSatisfiesRestAdapter is a compile-time assertion
// (adapter.go) made runtime-visible: if this package ever stops
// satisfying rest.Adapter, this test's package fails to build, not
// just this function.
func TestPostgresAdapterSatisfiesRestAdapter(t *testing.T) {
	var _ rest.Adapter = (*postgresAdapter)(nil)
}
