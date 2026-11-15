package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestCompileWhereScalarAndNestedNegation(t *testing.T) {
	pool := testPool(t)
	table := "where_semantics_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	setupDocTable(t, pool, table)
	if err := EnsureFunctions(context.Background(), pool); err != nil {
		t.Fatal(err)
	}
	insertDocs(t, pool, table,
		`{"name":"missing"}`, `{"name":"null","x":null,"when":null}`, `{"name":"empty","x":"","when":""}`,
		`{"name":"match","x":"yes","when":"2020-01-02T00:00:00Z"}`, `{"name":"other","x":"no","when":"2020-01-01T00:00:00Z"}`,
		`{"name":"invalid","x":7,"when":"not-a-date"}`)
	schema := SchemaSpec{Fields: map[string]Kind{"when": KindDateTime}}
	leaves := []Query{{"x": "yes"}, {"x": Query{"$eq": nil}}, {"when": Query{"$gt": "2020-01-01T12:00:00Z"}}}
	for _, q := range leaves {
		old, args, err := CompileFilter(q, schema, 0)
		if err != nil {
			t.Fatal(err)
		}
		optimized, otherArgs, err := CompileWhere(q, schema, 0)
		if err != nil || !reflect.DeepEqual(args, otherArgs) {
			t.Fatalf("bindings: %v", err)
		}
		rows, err := pool.Query(context.Background(), "SELECT doc->>'name', "+old+", "+optimized+" FROM "+table, args...)
		if err != nil {
			t.Fatal(err)
		}
		sawNull := false
		for rows.Next() {
			var name string
			var normalized bool
			var predicate *bool
			if err := rows.Scan(&name, &normalized, &predicate); err != nil {
				rows.Close()
				t.Fatalf("legacy boolean changed/null: %v", err)
			}
			truth := predicate != nil && *predicate
			if normalized != truth {
				rows.Close()
				t.Fatalf("scalar truth changed for %s", name)
			}
			sawNull = sawNull || predicate == nil
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		if !sawNull {
			t.Fatal("WHERE-only nullable contract was not exercised")
		}
		namesMatching(t, pool, table, q, schema)
		namesMatching(t, pool, table, Query{"$not": q}, schema)
		namesMatching(t, pool, table, Query{"$not": Query{"$not": q}}, schema)
	}
	// Cross product mixes nullable comparisons with already-normalized operators.
	terms := []Query{{"x": Query{"$eq": nil}}, {"x": Query{"$gt": "no"}}, {"x": Query{"$eq": "yes", "$exists": true}}, {"x": "yes"}, {"x": Query{"$ne": "yes"}}, {"x": Query{"$exists": true}}, {"when": Query{"$gt": "2020-01-01T12:00:00Z"}}}
	for _, a := range terms {
		for _, b := range terms {
			for _, op := range []string{"$and", "$or"} {
				combined := Query{op: []Query{a, b}}
				namesMatching(t, pool, table, combined, schema)
				namesMatching(t, pool, table, Query{"$not": combined}, schema)
				namesMatching(t, pool, table, Query{"$not": Query{op: []Query{{"$not": a}, b}}}, schema)
			}
		}
	}
}

func TestCompileWhereQuotedInputsAndOffset(t *testing.T) {
	pool := testPool(t)
	table := "where_quoted_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	setupDocTable(t, pool, table)
	field := "quo'te\\雪; DROP TABLE comments; --"
	value := "x' OR true; --\\🙂"
	doc := map[string]any{"name": "match", field: value, "meta": map[string]any{field: value}}
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	insertDocs(t, pool, table, string(raw), `{"name":"other"}`)
	for _, q := range []Query{{field: value}, {"meta." + field: value}} {
		namesMatching(t, pool, table, q, SchemaSpec{})
		sql, args, err := CompileWhere(q, SchemaSpec{}, 1)
		if err != nil {
			t.Fatal(err)
		}
		all := append([]any{"offset proof"}, args...)
		var got string
		if err := pool.QueryRow(context.Background(), fmt.Sprintf("SELECT $1::text FROM %s WHERE %s", table, sql), all...).Scan(&got); err != nil || got != "offset proof" {
			t.Fatalf("unsafe input/offset: got=%s err=%v", got, err)
		}
	}
}
