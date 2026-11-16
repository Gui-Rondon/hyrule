package restpostgres

import (
	"fmt"
	"strconv"

	"github.com/google/uuid"

	"github.com/Gui-Rondon/hyrule/platform/postgres"
	"github.com/Gui-Rondon/hyrule/rest"
)

// WriteLookup builds the CAS condition a write conditions on, from the
// document read before the write (original) -- always from original,
// never from a client's match/SkipConcurrency (SkipConcurrency only
// skips the HTTP-level If-Match requirement, never this).
func (a *postgresAdapter) WriteLookup(original rest.Doc) (any, error) {
	etag, _ := original["_etag"].(string)
	v, err := strconv.ParseInt(etag, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("restpostgres: invalid stored _etag %q: %w", original["_etag"], err)
	}
	return postgresLookup{ID: original["_id"], Version: v}, nil
}

// EqualsFilter builds "field == value", optionally excluding one
// document by excludeID -- the friendly pre-check checkUnique uses
// before a write; the real uniqueness guarantee is the index itself
// (23505), this only produces the nicer error message ahead of time.
func (a *postgresAdapter) EqualsFilter(field string, value any, excludeID any) (any, error) {
	f := equalsFilter{query: postgres.Query{field: value}}
	if excludeID != nil {
		id, ok := excludeID.(uuid.UUID)
		if !ok {
			return nil, fmt.Errorf("restpostgres: excludeID must be a uuid.UUID, got %T", excludeID)
		}
		f.excludeID = &id
	}
	return f, nil
}

// MergePatch applies changes to original as an RFC 7396 JSON Merge
// Patch: a null value deletes the key, a non-object value replaces it
// (arrays included -- never merged element-wise), and an object value
// merges recursively. Only original's business fields (never its meta
// fields) are patch targets; the result keeps original's meta fields
// untouched -- the caller validates the result against the full
// Schema and computes new meta fields (_etag/_updated) itself, via
// UpdateOne's RETURNING.
func (a *postgresAdapter) MergePatch(original, changes rest.Doc) (rest.Doc, error) {
	merged := mergePatchValue(businessFieldsOnly(original), changes)
	mergedFields, ok := merged.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("restpostgres: MergePatch: changes must be a JSON object, got %T", changes)
	}
	result := make(rest.Doc, len(mergedFields)+len(metaFields))
	for k, v := range mergedFields {
		result[k] = v
	}
	for _, k := range metaFields {
		if v, ok := original[k]; ok {
			result[k] = v
		}
	}
	return result, nil
}

// mergePatchValue is RFC 7396's MergePatch(Target, Patch) algorithm.
// Every value that ends up in the result is deep-copied off of target
// and patch, never aliased: a map/slice is a reference type in Go, so
// copying target's map by assignment alone would let the caller
// mutate the result and silently corrupt original through it -- a
// real risk here, since a later OnUpdated hook (Fase 4) receives both
// updated and original together.
func mergePatchValue(target, patch any) any {
	patchObj, ok := patch.(map[string]any)
	if !ok {
		return deepCopyJSON(patch) // a non-object patch value replaces target wholesale
	}
	targetObj, ok := target.(map[string]any)
	if !ok {
		targetObj = map[string]any{} // target isn't an object -- start from empty, per the RFC
	}
	result := make(map[string]any, len(targetObj)+len(patchObj))
	for k, v := range targetObj {
		result[k] = deepCopyJSON(v)
	}
	for k, v := range patchObj {
		if v == nil {
			delete(result, k)
			continue
		}
		result[k] = mergePatchValue(result[k], v)
	}
	return result
}

// deepCopyJSON copies v, recursively, for the map/slice shapes
// encoding/json ever decodes into -- a scalar is a value type in Go
// already, so it's returned as-is.
func deepCopyJSON(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			out[k] = deepCopyJSON(val)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, val := range t {
			out[i] = deepCopyJSON(val)
		}
		return out
	default:
		return v
	}
}
