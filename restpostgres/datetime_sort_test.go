package restpostgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/Gui-Rondon/hyrule/platform/postgres"
	"github.com/Gui-Rondon/hyrule/rest"
)

func TestSafeDateTimeSortOptIn(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	const table = "safe_datetime_sort_test"
	setupResourceTable(t, pool, table)
	if err := postgres.EnsureFunctions(ctx, pool); err != nil {
		t.Fatal(err)
	}
	fixtures := []struct {
		label   string
		value   any
		missing bool
	}{
		{label: "missing", missing: true}, {label: "null"}, {label: "bad", value: "bad"}, {label: "empty", value: ""},
		{label: "calendar", value: "2020-02-30T00:00:00Z"}, {label: "date-only", value: "2020-01-01"},
		{label: "no-zone", value: "2020-01-01T00:00:00"}, {label: "infinity", value: "infinity"},
		{label: "zero-year", value: "0000-01-01T00:00:00Z"}, {label: "number", value: 42}, {label: "boolean", value: true},
		{label: "object", value: map[string]any{}}, {label: "array", value: []any{}},
		{label: "utc", value: "2020-01-01T00:00:00Z"}, {label: "offset", value: "2019-12-31T21:00:00-03:00"},
		{label: "fraction", value: "2020-01-01T00:00:00.123456789Z"},
		{label: "24-hour", value: "2020-01-01T24:00:00Z"}, {label: "leap-second", value: "2020-01-01T00:00:60Z"},
	}
	originals := map[string]map[string]any{}
	for i, f := range fixtures {
		doc := map[string]any{"label": f.label, "tie": i}
		if !f.missing {
			doc["when"] = f.value
		}
		originals[f.label] = doc
		raw, err := json.Marshal(doc)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, "INSERT INTO "+table+"(doc) VALUES ($1::jsonb)", raw); err != nil {
			t.Fatal(err)
		}
	}
	for _, safe := range []bool{false, true} {
		a, err := NewAdapter(postgres.NewStore(pool, table), postgres.Transactor{Pool: pool}, Options{Schema: rest.Schema{"when": {Type: rest.TypeDatetime}, "tie": {Type: rest.TypeInteger}}, Retry: rest.DefaultRetryOptions(), SafeDateTimeSort: safe})
		if err != nil {
			t.Fatal(err)
		}
		for _, desc := range []bool{false, true} {
			terms := []SortTerm{{Field: "when", Desc: desc}, {Field: "tie"}}
			docs, err := a.Find(ctx, nil, terms, nil, 0, 100)
			if !safe {
				var pe *pgconn.PgError
				if !errors.As(err, &pe) || pe.Code != "22007" {
					t.Fatalf("default cast must still fail: %v", err)
				}
				continue
			}
			if err != nil || len(docs) != len(fixtures) {
				t.Fatalf("safe rows=%d: %v", len(docs), err)
			}
			dir := "ASC"
			if desc {
				dir = "DESC"
			}
			rows, err := pool.Query(ctx, "SELECT doc->>'label' FROM "+table+" ORDER BY hyrule_safe_timestamptz(doc->>'when') "+dir+", (doc->>'tie')::int8")
			if err != nil {
				t.Fatal(err)
			}
			var labels []string
			for rows.Next() {
				var label string
				if err := rows.Scan(&label); err != nil {
					rows.Close()
					t.Fatal(err)
				}
				labels = append(labels, label)
			}
			rows.Close()
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			for i, doc := range docs {
				label := fmt.Sprint(doc["label"])
				if label != labels[i] {
					t.Fatalf("row %d=%s want=%s", i, label, labels[i])
				}
				value, present := doc["when"]
				want, wantPresent := originals[label]["when"]
				raw, err := json.Marshal(value)
				if err != nil {
					t.Fatal(err)
				}
				expected, err := json.Marshal(want)
				if err != nil {
					t.Fatal(err)
				}
				if present != wantPresent || string(raw) != string(expected) {
					t.Fatalf("stored value changed for %s: %s / %s", label, raw, expected)
				}
			}
			// The same explicitly ordered page must be preserved through adapter pagination.
			page, err := a.Find(ctx, nil, terms, nil, 2, 5)
			if err != nil || len(page) != 5 {
				t.Fatalf("page: %v", err)
			}
			for i, d := range page {
				if d["_id"] != docs[i+2]["_id"] {
					t.Fatal("page order changed")
				}
			}
		}
	}
}
