package postgres

import (
	"reflect"
	"testing"
)

func TestCompileFilterEmpty(t *testing.T) {
	where, args, err := CompileFilter(nil, SchemaSpec{}, 0)
	if err != nil || where != "TRUE" || len(args) != 0 {
		t.Fatalf("CompileFilter(nil) = %q, %v, %v", where, args, err)
	}
	where, args, err = CompileFilter(Query{}, SchemaSpec{}, 0)
	if err != nil || where != "TRUE" || len(args) != 0 {
		t.Fatalf("CompileFilter({}) = %q, %v, %v", where, args, err)
	}
}

func TestCompileFilterImplicitEq(t *testing.T) {
	where, args, err := CompileFilter(Query{"status": "published"}, SchemaSpec{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if want := `(doc->$1::text = to_jsonb($2::text)) IS TRUE`; where != want {
		t.Fatalf("where = %q, want %q", where, want)
	}
	if !reflect.DeepEqual(args, []any{"status", "published"}) {
		t.Fatalf("args = %v", args)
	}
}

func TestCompileFilterEqNull(t *testing.T) {
	// $eq: null compiles against the 'null'::jsonb literal, no extra
	// bind parameter for the value (postgres-rfc.md §10.1).
	where, args, err := CompileFilter(Query{"status": Query{"$eq": nil}}, SchemaSpec{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if want := `(doc->$1::text = 'null'::jsonb) IS TRUE`; where != want {
		t.Fatalf("where = %q, want %q", where, want)
	}
	if !reflect.DeepEqual(args, []any{"status"}) {
		t.Fatalf("args = %v", args)
	}
}

func TestCompileFilterNeNull(t *testing.T) {
	where, args, err := CompileFilter(Query{"status": Query{"$ne": nil}}, SchemaSpec{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if want := `(doc->$1::text IS DISTINCT FROM 'null'::jsonb) IS TRUE`; where != want {
		t.Fatalf("where = %q, want %q", where, want)
	}
	if !reflect.DeepEqual(args, []any{"status"}) {
		t.Fatalf("args = %v", args)
	}
}

func TestCompileFilterKindCast(t *testing.T) {
	schema := SchemaSpec{Fields: map[string]Kind{"views": KindInteger}}
	where, args, err := CompileFilter(Query{"views": Query{"$gt": 10}}, schema, 0)
	if err != nil {
		t.Fatal(err)
	}
	if want := `(doc->$1::text > to_jsonb($2::int8)) IS TRUE`; where != want {
		t.Fatalf("where = %q, want %q", where, want)
	}
	if !reflect.DeepEqual(args, []any{"views", 10}) {
		t.Fatalf("args = %v", args)
	}
}

func TestCompileFilterComparisonNullRejected(t *testing.T) {
	for _, op := range []string{"$gt", "$gte", "$lt", "$lte"} {
		if _, _, err := CompileFilter(Query{"views": Query{op: nil}}, SchemaSpec{}, 0); err == nil {
			t.Errorf("%s: null expected to be rejected", op)
		}
	}
}

func TestCompileFilterInEmptyNeverMatches(t *testing.T) {
	// Must not consume a bind parameter it then never references.
	where, args, err := CompileFilter(Query{"tags": Query{"$in": []any{}}}, SchemaSpec{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if where != "FALSE" || len(args) != 0 {
		t.Fatalf("where, args = %q, %v, want FALSE, []", where, args)
	}
}

func TestCompileFilterNinEmptyAlwaysMatches(t *testing.T) {
	where, args, err := CompileFilter(Query{"tags": Query{"$nin": []any{}}}, SchemaSpec{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if where != "TRUE" || len(args) != 0 {
		t.Fatalf("where, args = %q, %v, want TRUE, []", where, args)
	}
}

func TestCompileFilterInNonEmpty(t *testing.T) {
	where, args, err := CompileFilter(Query{"status": Query{"$in": []any{"a", "b"}}}, SchemaSpec{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	want := `(EXISTS (SELECT 1 FROM unnest(ARRAY[to_jsonb($2::text), to_jsonb($3::text)]::jsonb[]) AS item(value) WHERE doc->$1::text IS NOT DISTINCT FROM item.value)) IS TRUE`
	if where != want {
		t.Fatalf("where = %q, want %q", where, want)
	}
	if !reflect.DeepEqual(args, []any{"status", "a", "b"}) {
		t.Fatalf("args = %v", args)
	}
}

func TestCompileFilterExists(t *testing.T) {
	where, args, err := CompileFilter(Query{"slug": Query{"$exists": true}}, SchemaSpec{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if want := `(doc ? $1::text) IS TRUE`; where != want {
		t.Fatalf("where = %q, want %q", where, want)
	}
	// Exactly one placeholder is used -- the bug this test guards
	// against consumed a stray extra one via docRef before dispatch.
	if !reflect.DeepEqual(args, []any{"slug"}) {
		t.Fatalf("args = %v, want exactly one argument", args)
	}
}

func TestCompileFilterExistsFalse(t *testing.T) {
	where, args, err := CompileFilter(Query{"slug": Query{"$exists": false}}, SchemaSpec{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if want := `(NOT (doc ? $1::text)) IS TRUE`; where != want {
		t.Fatalf("where = %q, want %q", where, want)
	}
	if !reflect.DeepEqual(args, []any{"slug"}) {
		t.Fatalf("args = %v", args)
	}
}

func TestCompileFilterHas(t *testing.T) {
	where, args, err := CompileFilter(Query{"tags": Query{"$has": "go"}}, SchemaSpec{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	want := `(CASE WHEN jsonb_typeof(doc->$1::text) = 'array' THEN doc->$1::text @> jsonb_build_array($2::text) ELSE FALSE END)`
	if where != want {
		t.Fatalf("where = %q, want %q", where, want)
	}
	if !reflect.DeepEqual(args, []any{"tags", "go"}) {
		t.Fatalf("args = %v", args)
	}
}

func TestCompileFilterNestedPathEq(t *testing.T) {
	where, args, err := CompileFilter(Query{"meta.author": Query{"$eq": "ganon"}}, SchemaSpec{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	want := `(doc #> $1::text[] = to_jsonb($2::text)) IS TRUE`
	if where != want {
		t.Fatalf("where = %q, want %q", where, want)
	}
	if len(args) != 2 || !reflect.DeepEqual(args[0], []string{"meta", "author"}) || args[1] != "ganon" {
		t.Fatalf("args = %v", args)
	}
}

func TestCompileFilterNestedPathUnsupportedOperator(t *testing.T) {
	if _, _, err := CompileFilter(Query{"meta.author": Query{"$gt": 1}}, SchemaSpec{}, 0); err == nil {
		t.Fatal("expected an error: nested paths support only $exists/$eq/$ne")
	}
}

func TestCompileFilterNestedPathMultipleOperatorsRejected(t *testing.T) {
	if _, _, err := CompileFilter(Query{"meta.author": Query{"$eq": "a", "$ne": "b"}}, SchemaSpec{}, 0); err == nil {
		t.Fatal("expected an error: nested paths accept exactly one operator")
	}
}

func TestCompileFilterNot(t *testing.T) {
	schema := SchemaSpec{Fields: map[string]Kind{"views": KindInteger}}
	where, args, err := CompileFilter(Query{"$not": Query{"views": Query{"$gt": 100}}}, schema, 0)
	if err != nil {
		t.Fatal(err)
	}
	want := `(NOT ((doc->$1::text > to_jsonb($2::int8)) IS TRUE))`
	if where != want {
		t.Fatalf("where = %q, want %q", where, want)
	}
	if !reflect.DeepEqual(args, []any{"views", 100}) {
		t.Fatalf("args = %v", args)
	}
}

func TestCompileFilterAndOr(t *testing.T) {
	schema := SchemaSpec{Fields: map[string]Kind{"views": KindInteger}}
	where, _, err := CompileFilter(Query{"$and": []Query{
		{"status": "published"},
		{"$or": []Query{{"views": Query{"$gt": 10}}, {"views": Query{"$lt": 0}}}},
	}}, schema, 0)
	if err != nil {
		t.Fatal(err)
	}
	want := `((doc->$1::text = to_jsonb($2::text)) IS TRUE AND ((doc->$3::text > to_jsonb($4::int8)) IS TRUE OR (doc->$5::text < to_jsonb($6::int8)) IS TRUE))`
	if where != want {
		t.Fatalf("where = %q, want %q", where, want)
	}
}

func TestCompileFilterAndOrEmptyListRejected(t *testing.T) {
	if _, _, err := CompileFilter(Query{"$and": []Query{}}, SchemaSpec{}, 0); err == nil {
		t.Fatal("expected an error for an empty $and list")
	}
}

func TestCompileFilterMultipleTopLevelKeysAreAnded(t *testing.T) {
	schema := SchemaSpec{Fields: map[string]Kind{"a": KindInteger, "b": KindInteger}}
	where, args, err := CompileFilter(Query{"a": 1, "b": 2}, schema, 0)
	if err != nil {
		t.Fatal(err)
	}
	// Keys are sorted for deterministic SQL text.
	want := `((doc->$1::text = to_jsonb($2::int8)) IS TRUE AND (doc->$3::text = to_jsonb($4::int8)) IS TRUE)`
	if where != want {
		t.Fatalf("where = %q, want %q", where, want)
	}
	if !reflect.DeepEqual(args, []any{"a", 1, "b", 2}) {
		t.Fatalf("args = %v", args)
	}
}

func TestCompileFilterMultipleOperatorsOnOneFieldAreAnded(t *testing.T) {
	schema := SchemaSpec{Fields: map[string]Kind{"views": KindInteger}}
	where, args, err := CompileFilter(Query{"views": Query{"$gte": 1, "$lte": 10}}, schema, 0)
	if err != nil {
		t.Fatal(err)
	}
	want := `((doc->$1::text >= to_jsonb($2::int8)) IS TRUE AND (doc->$3::text <= to_jsonb($4::int8)) IS TRUE)`
	if where != want {
		t.Fatalf("where = %q, want %q", where, want)
	}
	if !reflect.DeepEqual(args, []any{"views", 1, "views", 10}) {
		t.Fatalf("args = %v", args)
	}
}

func TestCompileFilterStartAtOffsetsPlaceholders(t *testing.T) {
	where, args, err := CompileFilter(Query{"status": "x"}, SchemaSpec{}, 3)
	if err != nil {
		t.Fatal(err)
	}
	if want := `(doc->$4::text = to_jsonb($5::text)) IS TRUE`; where != want {
		t.Fatalf("where = %q, want %q", where, want)
	}
	if !reflect.DeepEqual(args, []any{"status", "x"}) {
		t.Fatalf("args = %v", args)
	}
}

func TestCompileFilterKindMismatchRejected(t *testing.T) {
	// A Go int against KindString's "text" cast would otherwise reach
	// pgx and fail there with a low-level "cannot find encode plan"
	// message -- checkKindValue catches it here instead, as a clear
	// compiler error, before any query is sent.
	if _, _, err := CompileFilter(Query{"status": 100}, SchemaSpec{}, 0); err == nil {
		t.Fatal("expected an error: int value against the default KindString cast")
	}
	schema := SchemaSpec{Fields: map[string]Kind{"views": KindInteger}}
	if _, _, err := CompileFilter(Query{"views": "not an int"}, schema, 0); err == nil {
		t.Fatal("expected an error: string value against a KindInteger cast")
	}
	// A matching Go type is accepted.
	if _, _, err := CompileFilter(Query{"views": 10}, schema, 0); err != nil {
		t.Fatalf("unexpected error for a matching int/KindInteger pair: %v", err)
	}
}

func TestCheckKindValue(t *testing.T) {
	cases := []struct {
		val  any
		kind Kind
		ok   bool
	}{
		{"x", KindString, true},
		{1, KindString, false},
		{"2024-01-02T00:00:00Z", KindDateTime, true},
		{1, KindDateTime, false},
		{1, KindNumber, true},
		{1.5, KindNumber, true},
		{"1.5", KindNumber, false},
		{true, KindBoolean, true},
		{"true", KindBoolean, false},
		// KindArray/KindObject have no enforced Go representation yet
		// -- the DSL's documented operators ($eq/$has*/$in) don't name
		// one, so any value passes.
		{1, KindArray, true},
		{1, KindObject, true},
	}
	for _, c := range cases {
		err := checkKindValue(c.val, c.kind)
		if (err == nil) != c.ok {
			t.Errorf("checkKindValue(%v, %v) error = %v, want ok=%v", c.val, c.kind, err, c.ok)
		}
	}
}

func TestCompileFilterUnsupportedOperator(t *testing.T) {
	if _, _, err := CompileFilter(Query{"status": Query{"$regex": "x"}}, SchemaSpec{}, 0); err == nil {
		t.Fatal("expected an error for an unsupported operator")
	}
}

func TestCompileFilterWrongShapeErrors(t *testing.T) {
	cases := []Query{
		{"$not": "not a Query"},
		{"$and": "not a []Query"},
		{"status": Query{"$in": "not a slice"}},
		{"status": Query{"$exists": "not a bool"}},
	}
	for i, q := range cases {
		if _, _, err := CompileFilter(q, SchemaSpec{}, 0); err == nil {
			t.Errorf("case %d: %v: expected an error", i, q)
		}
	}
}
