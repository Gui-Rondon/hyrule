package restpostgres

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/Gui-Rondon/hyrule/rest"
)

func FuzzMergePatch(f *testing.F) {
	f.Add(`{"a":{"b":1},"items":[1,2]}`, `{"a":{"b":null},"items":[3]}`)
	f.Add(`{"a":1,"_etag":"7"}`, `{"a":{"b":2},"_etag":"attacker"}`)
	f.Add(`{}`, `{"a":null}`)
	f.Fuzz(func(t *testing.T, targetJSON, patchJSON string) {
		if len(targetJSON)+len(patchJSON) > 4096 {
			t.Skip()
		}
		var target, patch map[string]any
		if json.Unmarshal([]byte(targetJSON), &target) != nil || target == nil ||
			json.Unmarshal([]byte(patchJSON), &patch) != nil || patch == nil {
			return
		}
		target["_etag"] = "original"
		beforeTarget, _ := json.Marshal(target)
		beforePatch, _ := json.Marshal(patch)
		adapter := &postgresAdapter{}
		merged, err := adapter.MergePatch(rest.Doc(target), rest.Doc(patch))
		if err != nil {
			t.Fatal(err)
		}
		if merged["_etag"] != "original" {
			t.Fatal("patch overwrote metadata")
		}
		for key, value := range patch {
			if isFuzzMeta(key) {
				continue
			}
			if value == nil {
				if _, exists := merged[key]; exists {
					t.Fatal("null did not delete field")
				}
			} else if _, object := value.(map[string]any); !object && !reflect.DeepEqual(merged[key], value) {
				t.Fatal("scalar/array replacement differs from patch")
			}
		}
		// Applying a JSON merge patch twice must be idempotent.
		again, err := adapter.MergePatch(merged, rest.Doc(patch))
		if err != nil || !reflect.DeepEqual(merged, again) {
			t.Fatal("merge patch is not idempotent")
		}
		// Mutate all nested output containers; inputs must remain independent.
		mutateFuzzJSON(map[string]any(merged))
		afterTarget, _ := json.Marshal(target)
		afterPatch, _ := json.Marshal(patch)
		if string(beforeTarget) != string(afterTarget) || string(beforePatch) != string(afterPatch) {
			t.Fatal("merge result aliases an input")
		}
	})
}

func isFuzzMeta(key string) bool {
	for _, field := range metaFields {
		if key == field {
			return true
		}
	}
	return false
}

func mutateFuzzJSON(v any) {
	switch value := v.(type) {
	case map[string]any:
		for _, child := range value {
			mutateFuzzJSON(child)
		}
		value["fuzz_mutation"] = true
	case []any:
		for _, child := range value {
			mutateFuzzJSON(child)
		}
		if len(value) > 0 {
			value[0] = "fuzz_mutation"
		}
	}
}
