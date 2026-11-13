package restpostgres

import (
	"reflect"
	"testing"

	"github.com/Gui-Rondon/hyrule/rest"
)

func TestApplyProjectionEmptyKeepsEverything(t *testing.T) {
	doc := rest.Doc{"_id": "x", "name": "a", "secret": "s"}
	got := applyProjection(doc, nil)
	if !reflect.DeepEqual(got, doc) {
		t.Fatalf("applyProjection(nil) = %#v, want doc unchanged", got)
	}
}

func TestApplyProjectionNarrows(t *testing.T) {
	doc := rest.Doc{"_id": "x", "_etag": "1", "name": "a", "secret": "s"}
	got := applyProjection(doc, Projection{"name": true})
	if _, ok := got["secret"]; ok {
		t.Fatalf("got = %#v, secret must be excluded", got)
	}
	if got["name"] != "a" {
		t.Fatalf("got[name] = %v, want a", got["name"])
	}
	if got["_id"] != "x" || got["_etag"] != "1" {
		t.Fatalf("got = %#v, meta fields must always be kept", got)
	}
}

func TestApplyProjectionFalseExcludes(t *testing.T) {
	doc := rest.Doc{"_id": "x", "name": "a", "secret": "s"}
	got := applyProjection(doc, Projection{"name": true, "secret": false})
	if _, ok := got["secret"]; ok {
		t.Fatalf("got = %#v, secret: false must exclude it", got)
	}
}

func TestApplyProjectionMissingFieldIsSkipped(t *testing.T) {
	doc := rest.Doc{"_id": "x", "name": "a"}
	got := applyProjection(doc, Projection{"nonexistent": true})
	if _, ok := got["nonexistent"]; ok {
		t.Fatalf("got = %#v, a field absent from doc must not appear", got)
	}
}
