package rest

import (
	"reflect"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// TestMongoAdapterQueryAdapter exercises mongoAdapter's QueryAdapter
// methods directly, against the free combineFilter/combineProjection/
// combineSort/writeLookup/parseWhere functions they wrap -- Service
// (Fase 4a) now calls these same methods through s.res.Adapter for
// every request, but this direct test stays as the most precise place
// to pin the adapter's own behavior down to the function it forwards
// to, independent of Service's own plumbing (covered by
// service_test.go).
func TestMongoAdapterQueryAdapter(t *testing.T) {
	// A bare mongoAdapter{}: every method exercised here takes res (or
	// no state at all) explicitly, so it never needs the store/res
	// fields setDefaults would otherwise wire up.
	adapter := &mongoAdapter{}
	res := &Resource{
		Adapter:    adapter,
		SoftDelete: true,
		Datasource: Datasource{
			Filter:      bson.M{"kind": "article"},
			DefaultSort: bson.D{{Key: "name", Value: 1}},
			Projection:  bson.M{"name": 1, "slug": 1},
		},
	}
	res.setDefaults()

	t.Run("ParseWhere", func(t *testing.T) {
		got, err := adapter.ParseWhere(`{"_id":"6ada449701ecde6e488b8ac9"}`, res)
		if err != nil {
			t.Fatal(err)
		}
		want, err := parseWhere(`{"_id":"6ada449701ecde6e488b8ac9"}`, res.QueryDenylist, res.QueryObjectIDAsString)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("ParseWhere via adapter = %#v, want %#v", got, want)
		}
		oid, _ := bson.ObjectIDFromHex("6ada449701ecde6e488b8ac9")
		if got.(bson.M)["_id"] != oid {
			t.Errorf("_id = %#v, want an ObjectID", got.(bson.M)["_id"])
		}
	})

	t.Run("ParseWhere honors QueryObjectIDAsString", func(t *testing.T) {
		strRes := &Resource{Adapter: adapter, QueryObjectIDAsString: true}
		strRes.setDefaults()
		got, err := adapter.ParseWhere(`{"_id":"6ada449701ecde6e488b8ac9"}`, strRes)
		if err != nil {
			t.Fatal(err)
		}
		if got.(bson.M)["_id"] != "6ada449701ecde6e488b8ac9" {
			t.Errorf("_id = %#v, want the hex string kept as-is", got.(bson.M)["_id"])
		}
	})

	t.Run("ParseSort", func(t *testing.T) {
		got, err := adapter.ParseSort("-name,slug")
		if err != nil {
			t.Fatal(err)
		}
		want, _ := ParseSort("-name,slug")
		if !reflect.DeepEqual(got, want) {
			t.Errorf("ParseSort via adapter = %#v, want %#v", got, want)
		}
	})

	t.Run("ParseProjection", func(t *testing.T) {
		got, err := adapter.ParseProjection(`{"name":1}`)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, bson.M{"name": float64(1)}) {
			t.Errorf("ParseProjection via adapter = %#v", got)
		}
	})

	t.Run("CombineFilter", func(t *testing.T) {
		got, err := adapter.CombineFilter(res, bson.M{"slug": "go"}, false)
		if err != nil {
			t.Fatal(err)
		}
		want := combineFilter(res, bson.M{"slug": "go"}, false)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("CombineFilter via adapter = %#v, want %#v", got, want)
		}
		// a clientFilter that isn't a bson.M (e.g. nil, or some other
		// shape) must not panic -- treated as if nothing was sent.
		got, err = adapter.CombineFilter(res, nil, false)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, combineFilter(res, nil, false)) {
			t.Errorf("CombineFilter(nil) via adapter = %#v", got)
		}
	})

	t.Run("CombineSort falls back to DefaultSort", func(t *testing.T) {
		got := adapter.CombineSort(res, nil)
		if !reflect.DeepEqual(got, res.Datasource.DefaultSort) {
			t.Errorf("CombineSort(nil) = %#v, want DefaultSort", got)
		}
		clientSort := bson.D{{Key: "slug", Value: -1}}
		got = adapter.CombineSort(res, clientSort)
		if !reflect.DeepEqual(got, clientSort) {
			t.Errorf("CombineSort(clientSort) = %#v, want the client's own sort", got)
		}
	})

	t.Run("CombineProjection", func(t *testing.T) {
		got, err := adapter.CombineProjection(res, bson.M{"name": 1})
		if err != nil {
			t.Fatal(err)
		}
		want := combineProjection(res, bson.M{"name": 1})
		if !reflect.DeepEqual(got, want) {
			t.Errorf("CombineProjection via adapter = %#v, want %#v", got, want)
		}
	})

	t.Run("WriteLookup", func(t *testing.T) {
		original := Doc{"_id": "x", "_etag": "3"}
		got, err := adapter.WriteLookup(original)
		if err != nil {
			t.Fatal(err)
		}
		want := bson.M{"_id": "x", "_etag": "3"}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("WriteLookup via adapter = %#v, want %#v", got, want)
		}
	})
}
