package postgres

import (
	"encoding/json"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

var fuzzPlaceholder = regexp.MustCompile(`\$([0-9]+)`)

func FuzzCompileFilter(f *testing.F) {
	for _, seed := range []string{
		`{"name":"link"}`, `{"$or":[{"name":"a"},{"name":{"$ne":null}}]}`,
		`{"$not":{"nested.name":{"$exists":true}}}`, `{"name":{"$in":["a","b"]}}`,
		`{"'; DROP TABLE items; --":"' OR 1=1 --"}`, `{"$where":"return true"}`,
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		if len(raw) > 4096 {
			t.Skip()
		}
		var query Query
		if json.Unmarshal([]byte(raw), &query) == nil {
			before, _ := json.Marshal(query)
			for _, compile := range []func(Query, SchemaSpec, int) (string, []any, error){CompileFilter, CompileWhere} {
				where, args, err := compile(query, SchemaSpec{}, 3)
				if err == nil {
					seen := map[int]bool{}
					for _, match := range fuzzPlaceholder.FindAllStringSubmatch(where, -1) {
						n, _ := strconv.Atoi(match[1])
						if n < 4 || n > 3+len(args) {
							t.Fatalf("unbound placeholder %s in %s", match[0], where)
						}
						seen[n] = true
					}
					if len(seen) != len(args) {
						t.Fatalf("unused bindings: %s / %v", where, args)
					}
				}
			}
			after, _ := json.Marshal(query)
			if string(before) != string(after) {
				t.Fatal("compiler mutated the client query")
			}
		}
		// Independently vary field and value, including SQL metacharacters.
		// Neither may be interpolated into the SQL text.
		field, value := "hyrule_field_"+raw, "hyrule_value_"+raw
		where, args, err := CompileFilter(Query{field: value}, SchemaSpec{}, 0)
		if err != nil {
			t.Fatal(err)
		}
		other, _, err := CompileFilter(Query{field: "different value"}, SchemaSpec{}, 0)
		if err != nil || where != other || strings.Contains(where, "hyrule_field_") || strings.Contains(where, "hyrule_value_") {
			t.Fatal("client field/value affected SQL text")
		}
		if len(args) != 2 || !reflect.DeepEqual(args[1], value) {
			t.Fatalf("lost bound value: %v", args)
		}
	})
}
