package rest

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"reflect"
	"testing"
)

// Inputs are bounded to keep campaigns focused on parser/validation behavior,
// rather than allocating arbitrarily large request bodies.
func FuzzParseListQuery(f *testing.F) {
	for _, seed := range []string{
		`where={"name":"link"}&sort=-name&page=1&max_results=10`,
		`where={"$or":[{"$where":"return true"}]}`,
		`where=null`, `where={"n":1e999}&projection={"name":1}`,
		`page=-9223372036854775808&max_results=99999999999999999999`,
		`page=-2147483648`, `page=9223372036854775807`,
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		if len(raw) > 4096 {
			t.Skip()
		}
		args, err := url.ParseQuery(raw)
		if err != nil {
			return
		}
		q, err := ParseListQuery(args, Pagination{Default: 25, Limit: 50}, DefaultQueryDenylist)
		if err != nil {
			return
		}
		if q.MaxResults <= 0 || q.MaxResults > 50 || q.Page < 1 {
			t.Fatalf("invalid pagination: %+v", q)
		}
		// Independently walk the input JSON, rather than using checkOperators
		// as an oracle for itself. No accepted query may contain denied keys.
		var input any
		if json.Unmarshal([]byte(args.Get("where")), &input) == nil && containsDeniedFuzzKey(input) {
			t.Fatal("accepted a denied operator")
		}
		// Every denied operator is also tested under arbitrary client keys.
		for _, op := range DefaultQueryDenylist {
			payload, _ := json.Marshal(map[string]any{raw: []any{map[string]any{op: "1"}}})
			var denied ErrDeniedOperator
			if _, err := ParseWhere(string(payload), DefaultQueryDenylist); !errors.As(err, &denied) {
				t.Fatalf("nested %s was not rejected: %v", op, err)
			}
		}
	})
}

func containsDeniedFuzzKey(v any) bool {
	switch value := v.(type) {
	case map[string]any:
		for key, child := range value {
			if key == "$where" || key == "$function" || key == "$accumulator" || containsDeniedFuzzKey(child) {
				return true
			}
		}
	case []any:
		for _, child := range value {
			if containsDeniedFuzzKey(child) {
				return true
			}
		}
	}
	return false
}

func FuzzValidateDict(f *testing.F) {
	for _, seed := range []string{
		`{}`, `{"name":"link","count":2,"tags":["draft"]}`,
		`{"name":{},"count":1e999,"tags":[{},null]}`,
		`{"nested":{"name":1},"tags":[["draft"]]}`,
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		if len(raw) > 4096 {
			t.Skip()
		}
		var full, partial map[string]any
		if json.Unmarshal([]byte(raw), &full) != nil || full == nil {
			return
		}
		if err := json.Unmarshal([]byte(raw), &partial); err != nil {
			t.Fatal(err)
		}
		schema := Schema{
			"name":   {Type: TypeString, Required: true, MinLength: 1},
			"count":  {Type: TypeInteger},
			"tags":   {Type: TypeList, Items: &Field{Type: TypeString, Allowed: []any{"draft", "published"}}},
			"nested": {Type: TypeDict, Schema: Schema{"name": {Type: TypeString}}},
		}
		fullIssues := ValidateDict(context.Background(), schema, full, false)
		partialIssues := ValidateDictPartial(context.Background(), schema, partial, false)
		// Full and partial validation agree on supplied fields. Only full
		// validation may require an omitted name.
		if _, exists := partial["name"]; !exists {
			if fullIssues["name"] != "required field" {
				t.Fatal("full validation accepted missing required name")
			}
			delete(fullIssues, "name")
		}
		if !reflect.DeepEqual(fullIssues, partialIssues) {
			t.Fatalf("full/partial disagree: %v / %v", fullIssues, partialIssues)
		}
	})
}
