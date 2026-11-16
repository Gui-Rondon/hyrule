package rest

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"net/url"
	"reflect"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestCompatibleEtagsSupportsStoredMongoAndJSONArrays(t *testing.T) {
	svc, _ := newRetryTestService(RetryOptions{}, 0)
	svc.res.CompatibleEtags = "history"
	for _, value := range []any{bson.A{"old", 42}, []any{"old", false}, "old", nil} {
		doc := Doc{"_etag": "current", "history": value}
		match, _ := ParseIfMatch([]string{`"old"`})
		err := svc.checkEtag(doc, WriteOptions{IfMatch: match})
		switch value.(type) {
		case bson.A, []any:
			if err != nil {
				t.Fatalf("compatible tag rejected: %v", err)
			}
		default:
			var stale *PreconditionFailedError
			if !errors.As(err, &stale) {
				t.Fatalf("non-array history accepted: %v", err)
			}
		}
	}
}

func TestProjectionFlagsAcceptBSONIntegerWidths(t *testing.T) {
	for _, v := range []any{int32(1), int64(1), true} {
		if !isOne(v) || !truthyValue(v) {
			t.Fatalf("one flag rejected: %T", v)
		}
	}
	for _, v := range []any{int32(0), int64(0), false, "1", nil} {
		if isOne(v) || truthyValue(v) {
			t.Fatalf("zero/unsupported flag accepted: %T", v)
		}
	}
	if isOne(int32(2)) || !truthyValue(int64(-2)) {
		t.Fatal("projection inclusion and truthiness conflated")
	}
}

func TestDeepMergeUnderstandsBSONContainersWithoutMutatingOriginal(t *testing.T) {
	for _, nested := range []any{bson.M{"keep": "yes", "change": "before"}, bson.D{{Key: "keep", Value: "yes"}, {Key: "change", Value: "before"}}} {
		original := Doc{"nested": nested}
		got := deepMerge(original, Doc{"nested": map[string]any{"change": "after"}})
		if got["nested"].(map[string]any)["keep"] != "yes" || got["nested"].(map[string]any)["change"] != "after" {
			t.Fatalf("bad BSON merge: %v", got)
		}
		before, _ := asMap(nested)
		if before["change"] != "before" {
			t.Fatal("original nested BSON was mutated")
		}
	}
	got := deepMerge(Doc{"x": true}, Doc{"x": map[string]any{"key": "value"}})
	if !reflect.DeepEqual(got["x"], map[string]any{"key": "value"}) {
		t.Fatal("object failed to replace scalar")
	}
}

func TestPublicDynamicSchemaValidation(t *testing.T) {
	schema := Schema{"name": {Type: TypeString, Required: true}, "age": {Type: TypeInteger}}
	if issues := ValidateDict(context.Background(), schema, map[string]any{"age": json.Number("3")}, false); issues["name"] != "required field" {
		t.Fatalf("required check missing: %v", issues)
	}
	if issues := ValidateDictPartial(context.Background(), schema, map[string]any{"age": json.Number("3")}, false); len(issues) != 0 {
		t.Fatalf("partial validation rejected omitted required field: %v", issues)
	}
	if issues := ValidateDict(context.Background(), schema, map[string]any{"name": "valid", "extra": true}, true); len(issues) != 0 {
		t.Fatalf("allowUnknown rejected: %v", issues)
	}
}

func TestStoredItemLinksAndOpaqueIDs(t *testing.T) {
	self := map[string]any{"href": "items/id"}
	stored := map[string]any{"related": "items/other"}
	if got := itemLinks(stored, self); !reflect.DeepEqual(got["self"], self) || got["related"] != "items/other" {
		t.Fatal("stored links lost")
	}
	existing := map[string]any{"self": "custom"}
	if got := itemLinks(existing, self); got["self"] != "custom" {
		t.Fatal("custom self link overwritten")
	}
	if idString("slug") != "slug" || idString(int64(42)) != "42" {
		t.Fatal("opaque IDs encoded incorrectly")
	}
	args := url.Values{"fields": {"name"}}
	if got := RequestArgs(context.WithValue(context.Background(), argsKey{}, args)); got.Get("fields") != "name" {
		t.Fatal("request arguments lost")
	}
	if RequestArgs(context.Background()) != nil {
		t.Fatal("arguments invented outside request")
	}
	// Even errors serializing response payloads must keep the already selected status.
	rec := httptest.NewRecorder()
	WriteJSON(rec, 202, make(chan int))
	if rec.Code != 202 {
		t.Fatalf("status=%d", rec.Code)
	}
}

func TestParseListQueryRejectsInvalidPublicArguments(t *testing.T) {
	for key, value := range map[string]string{"where": "{", "sort": "{", "projection": "{"} {
		if _, err := ParseListQuery(url.Values{key: {value}}, Pagination{}, nil); err == nil {
			t.Errorf("accepted invalid %s", key)
		}
	}
}

func TestMongoIdentityLookupPreservesObjectID(t *testing.T) {
	a := &mongoAdapter{}
	id := bson.NewObjectID()
	if got := a.LookupID(id); got != id {
		t.Fatalf("identity = %v", got)
	}
}

func TestRetryBackoffBoundsWithInvalidConfiguration(t *testing.T) {
	for _, tc := range []struct{ base, max time.Duration }{{0, 0}, {-time.Second, -time.Second}, {time.Second, time.Millisecond}, {time.Second, time.Hour}} {
		for _, attempt := range []int{1, 2, 100} {
			delay := backoffWithJitter(attempt, tc.base, tc.max)
			bound := tc.max
			base := tc.base
			if base <= 0 {
				base = time.Millisecond
			}
			if bound < base {
				bound = base
			}
			if delay < 0 || delay > bound {
				t.Fatalf("attempt %d: delay %v outside [0,%v]", attempt, delay, bound)
			}
		}
	}
}
