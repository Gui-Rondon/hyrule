package rest

import (
	"context"
	"errors"
	"testing"
)

// mergedValidationSchema has a Required+Nullable field: the key must
// always be present, but its stored value may be null. RFC 7396 merge
// patch has no way to express "set this field to JSON null" without
// deleting the key -- a null in the PATCH body always means "remove
// it" -- so this is the one shape where a post-merge check
// (ValidatesMergedDocument) can ever catch something the pre-merge
// check (which only sees the client's PATCH body, where category:null
// is perfectly valid on its own) does not.
var mergedValidationSchema = Schema{
	"name":     {Type: TypeString, Required: true},
	"category": {Type: TypeString, Required: true, Nullable: true},
}

func newMergeValidationTestService(validatesMerged bool) (*Service, *fakeRetryAdapter) {
	adapter := &fakeRetryAdapter{
		doc:             Doc{"_id": "fake-id", "_etag": "1", "name": "a", "category": "x"},
		validatesMerged: validatesMerged,
	}
	res := &Resource{Adapter: adapter, Schema: mergedValidationSchema}
	res.setDefaults()
	return NewService(res, NoTransaction{}), adapter
}

// TestPatchValidatesMergedDocumentCatchesRequiredKeyDeletedByNull covers
// postgresAdapter's contract (ValidatesMergedDocument true): a PATCH
// that nulls a Required+Nullable field deletes the key via RFC 7396 --
// the merged document is now missing a required field, which only a
// post-merge check (section 9.4 of the Postgres portability plan) can
// catch; the pre-merge check alone (changes = {"category": nil}, which
// Nullable allows) would let it through.
func TestPatchValidatesMergedDocumentCatchesRequiredKeyDeletedByNull(t *testing.T) {
	svc, adapter := newMergeValidationTestService(true)

	_, err := svc.Patch(context.Background(), "fake-id", Doc{"category": nil}, WriteOptions{SkipConcurrency: true})
	var verr *ValidationError
	if !errors.As(err, &verr) {
		t.Fatalf("err = %v, want *ValidationError", err)
	}
	if _, ok := verr.Issues["category"]; !ok {
		t.Errorf("issues = %v, want an issue for category", verr.Issues)
	}
	if adapter.updateCalls != 0 {
		t.Errorf("UpdateOne called %d times, want 0 (the merged-document check must fail before any write)", adapter.updateCalls)
	}
}

// TestPatchWithoutValidatesMergedDocumentKeepsMongosContract covers
// mongoAdapter's contract (ValidatesMergedDocument false, the default):
// the very same request that 422s above must still succeed when the
// adapter never opts into the post-merge check -- Mongo's behavior is
// untouched by this feature.
func TestPatchWithoutValidatesMergedDocumentKeepsMongosContract(t *testing.T) {
	svc, adapter := newMergeValidationTestService(false)

	updated, err := svc.Patch(context.Background(), "fake-id", Doc{"category": nil}, WriteOptions{SkipConcurrency: true})
	if err != nil {
		t.Fatalf("Patch: %v", err)
	}
	if _, ok := updated["category"]; ok {
		t.Errorf("updated[category] = %v, want the key gone (RFC 7396 null still deletes it; only the extra validation is skipped)", updated["category"])
	}
	if adapter.updateCalls != 1 {
		t.Errorf("UpdateOne called %d times, want exactly 1", adapter.updateCalls)
	}
}

// TestPatchValidatesMergedDocumentSucceedsWhenNothingRequiredIsMissing
// is the plain, nothing-wrong baseline: Required+Nullable's key is
// simply replaced with a concrete value, never deleted, so the
// post-merge check (which only looks at Required key presence, never
// at a value's type/format) has nothing to flag.
func TestPatchValidatesMergedDocumentSucceedsWhenNothingRequiredIsMissing(t *testing.T) {
	svc, adapter := newMergeValidationTestService(true)

	_, err := svc.Patch(context.Background(), "fake-id", Doc{"category": "y"}, WriteOptions{SkipConcurrency: true})
	if err != nil {
		t.Fatalf("Patch: %v, want success (meta fields must never be treated as unknown fields)", err)
	}
	if adapter.updateCalls != 1 {
		t.Errorf("UpdateOne called %d times, want exactly 1", adapter.updateCalls)
	}
}

// TestPatchValidatesMergedDocumentIgnoresHookAddedFields covers a real
// regression found in /code-review on this feature: an OnUpdate hook
// is, by design, allowed to stamp a field the Schema never declares
// (behaviors/audit.Hooks()'s _updated_user is exactly this, and is
// exactly what the project's own cmd/hyrule scaffold wires up --
// Schema with no _updated_user, AllowUnknown false). That field was
// never subject to Schema validation before ValidatesMergedDocument
// existed (a hook only ever runs after the pre-merge check of the
// client's own PATCH body); it must still never be treated as
// "unknown field" now that this check sees the whole merged document.
func TestPatchValidatesMergedDocumentIgnoresHookAddedFields(t *testing.T) {
	adapter := &fakeRetryAdapter{
		doc:             Doc{"_id": "fake-id", "_etag": "1", "name": "a", "category": "x"},
		validatesMerged: true,
	}
	res := &Resource{
		Adapter: adapter,
		Schema:  mergedValidationSchema, // no _updated_user declared
		// AllowUnknown left false (the default, and the scaffold's).
	}
	res.Hooks.OnUpdate = []func(context.Context, Doc, Doc) error{
		func(_ context.Context, applied, _ Doc) error {
			applied["_updated_user"] = "fulano" // behaviors/audit.Hooks(), inlined
			return nil
		},
	}
	res.setDefaults()
	svc := NewService(res, NoTransaction{})

	_, err := svc.Patch(context.Background(), "fake-id", Doc{"category": "y"}, WriteOptions{SkipConcurrency: true})
	if err != nil {
		t.Fatalf("Patch: %v, want success (a hook-added field must never be treated as unknown)", err)
	}
	if adapter.updateCalls != 1 {
		t.Errorf("UpdateOne called %d times, want exactly 1", adapter.updateCalls)
	}
}

// TestPatchValidatesMergedDocumentNeverRevalidatesFieldTypes covers a
// second real regression found in /code-review: an earlier version of
// this check ran a full validateDict/checkType pass over the merged
// document, but updated's values are in internal/storage
// representation, never the raw client JSON checkType/convert expect
// -- a touched TypeInteger field is already int64 (prepareWrite's
// pre-merge convert() mutates changes in place before the merge), and
// an untouched TypeDatetime field read back from Postgres is an RFC
// 3339 string (jsonb's json.Marshal of the time.Time convert() stored
// it as), never Eve's DateFormat checkType's TypeDatetime case parses
// against. Both used to 422 on every single PATCH that didn't even
// touch them. The narrow, presence-only check must never look at a
// value's type or format at all.
func TestPatchValidatesMergedDocumentNeverRevalidatesFieldTypes(t *testing.T) {
	schema := Schema{
		"name":         {Type: TypeString, Required: true},
		"count":        {Type: TypeInteger, Required: true},
		"published_at": {Type: TypeDatetime, Required: true},
	}
	adapter := &fakeRetryAdapter{
		doc: Doc{
			"_id": "fake-id", "_etag": "1",
			"name":         "a",
			"count":        int64(5),               // already-converted representation
			"published_at": "2020-01-01T00:00:00Z", // RFC 3339, not Eve's DateFormat
		},
		validatesMerged: true,
	}
	res := &Resource{Adapter: adapter, Schema: schema}
	res.setDefaults()
	svc := NewService(res, NoTransaction{})

	_, err := svc.Patch(context.Background(), "fake-id", Doc{"name": "b"}, WriteOptions{SkipConcurrency: true})
	if err != nil {
		t.Fatalf("Patch: %v, want success (the post-merge check must never re-validate field types/formats)", err)
	}
}
