package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Gui-Rondon/hyrule/internal/pgexplain"
	"github.com/Gui-Rondon/hyrule/platform/postgres"
	"github.com/Gui-Rondon/hyrule/rest"
	"github.com/Gui-Rondon/hyrule/restpostgres"
)

// This gate exercises the real adapter and planner without provisioning services.
func TestIDSortPlans(t *testing.T) {
	if os.Getenv("POSTGRES_ID_SORT_TEST") != "1" {
		t.Skip("manual UUID ordering: make postgres-ha-id-sort-test")
	}
	uri := os.Getenv("POSTGRES_EXPLAIN_URI")
	if uri == "" {
		t.Fatal("POSTGRES_EXPLAIN_URI required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	report := &pgexplain.Report{CreatedUTC: time.Now().UTC(), RowsSeeded: 20000}
	_, file, _, _ := runtime.Caller(0)
	dir := filepath.Clean(filepath.Join(filepath.Dir(file), "../../../..", ".local/postgres-id-sort", time.Now().UTC().Format("20060102T150405Z")+"-"+uuid.NewString()))
	t.Logf("UUID sort artifacts: %s", dir)
	t.Cleanup(func() {
		report.Complete = !t.Failed() && len(report.Results) == 18 && len(report.RegressionChecks) == 12
		if t.Failed() {
			report.Errors = append(report.Errors, "test/setup/cleanup failed; see Go test output")
		}
		if err := report.Save(dir); err != nil {
			t.Error(err)
		}
	})
	pool := gatePool(t, uri, "idsort_", pgexplain.Tracer{})
	if err := pool.QueryRow(ctx, "SELECT version(),current_schema(),system_identifier::text FROM pg_control_system()").Scan(&report.ServerVersion, &report.Schema, &report.SystemID); err != nil {
		t.Fatal(err)
	}
	report.Settings = map[string]string{}
	for _, key := range []string{"enable_seqscan", "enable_indexscan", "enable_bitmapscan", "enable_indexonlyscan", "plan_cache_mode"} {
		var value string
		if err := pool.QueryRow(ctx, "SELECT current_setting($1)", key).Scan(&value); err != nil {
			t.Fatal(err)
		}
		report.Settings[key] = value
		if strings.HasPrefix(key, "enable_") && value != "on" {
			t.Fatalf("unforced planner required: %s=%s", key, value)
		}
	}
	var collation string
	if err := pool.QueryRow(ctx, "SELECT datcollate FROM pg_database WHERE datname=current_database()").Scan(&collation); err != nil {
		t.Fatal(err)
	}
	report.Settings["database_collation"] = collation
	// A quoted table name also checks that qualification reuses sanitized identifiers.
	const table = `id sort"fixture`
	quoted := pgx.Identifier{table}.Sanitize()
	if _, err := pool.Exec(ctx, `CREATE TABLE `+quoted+` (
 id uuid PRIMARY KEY, doc jsonb NOT NULL, created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now(), version bigint NOT NULL DEFAULT 1,
 deleted boolean NOT NULL DEFAULT false)`); err != nil {
		t.Fatal(err)
	}
	// Spread IDs over UUID byte boundaries and insert in reverse UUID order.
	// The last byte is nonzero as well, so both ends of the UUID participate.
	if _, err := pool.Exec(ctx, `INSERT INTO `+quoted+`(id,doc)
 SELECT (lpad(to_hex((i-1)::bigint*4294967295/19999),8,'0')||'-0000-4000-8000-'||lpad(to_hex(20001-i),12,'0'))::uuid,
 jsonb_build_object('group','all') FROM generate_series(20000,1,-1) i`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "ANALYZE "+quoted); err != nil {
		t.Fatal(err)
	}
	var pk string
	if err := pool.QueryRow(ctx, `SELECT c.relname FROM pg_index i JOIN pg_class c ON c.oid=i.indexrelid WHERE i.indrelid=$1::regclass AND i.indisprimary`, quoted).Scan(&pk); err != nil {
		t.Fatal(err)
	}
	index := postgres.IndexSpec{Table: table, Name: pk}
	for _, desc := range []bool{false, true} {
		for _, skip := range []int64{0, 10, 19990} {
			name := fmt.Sprintf("uuid-asc-page-%d", skip)
			if desc {
				name = fmt.Sprintf("uuid-desc-page-%d", skip)
			}
			c := pgexplain.Case{Name: name, Index: index, Filter: postgres.Query{"group": "all"}, Sort: []restpostgres.SortTerm{{Field: "_id", Desc: desc}}, Skip: skip, Limit: 10, WantRows: 10, Check: func(docs []rest.Doc) error {
				for j, doc := range docs {
					i := int(skip) + j + 1
					if desc {
						i = 20000 - int(skip) - j
					}
					want := fmt.Sprintf("%08x-0000-4000-8000-%012x", int64(i-1)*4294967295/19999, 20001-i)
					id, ok := doc["_id"].(uuid.UUID)
					if !ok || id.String() != want {
						return fmt.Errorf("row %d: id=%v want=%s", j, doc["_id"], want)
					}
				}
				return nil
			}}
			report.Scenarios = append(report.Scenarios, name)
			if err := report.Run(ctx, pool, c); err != nil {
				t.Fatal(err)
			}
			for _, result := range report.Results[len(report.Results)-3:] {
				// Deep OFFSET remains a result/order control: the optimizer
				// may prefer a sequential scan plus Sort at that cost.
				if skip < 19990 {
					requireIndexOrder(t, result.Plan, pk, desc)
					report.RegressionChecks = append(report.RegressionChecks, name+"/"+result.Mode+"/pk_without_sort")
				}
				t.Logf("%s %s: rows=%d indexes=%v buffers=%d/%d", name, result.Mode, result.Rows, result.UsedIndexes, result.SharedHits, result.SharedReads)
			}
		}
	}
}

func requireIndexOrder(t *testing.T, raw json.RawMessage, index string, desc bool) {
	t.Helper()
	var plans []struct{ Plan json.RawMessage }
	if err := json.Unmarshal(raw, &plans); err != nil || len(plans) != 1 {
		t.Fatalf("invalid plan: %v", err)
	}
	found := false
	var walk func(json.RawMessage)
	walk = func(raw json.RawMessage) {
		var node struct {
			Type      string            `json:"Node Type"`
			Index     string            `json:"Index Name"`
			Direction string            `json:"Scan Direction"`
			Plans     []json.RawMessage `json:"Plans"`
		}
		if err := json.Unmarshal(raw, &node); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(node.Type, "Sort") {
			t.Fatalf("index ordering requires Sort: %s", raw)
		}
		direction := "Forward"
		if desc {
			direction = "Backward"
		}
		if node.Index == index && node.Direction == direction {
			found = true
		}
		for _, child := range node.Plans {
			walk(child)
		}
	}
	walk(plans[0].Plan)
	if !found {
		t.Fatalf("missing %s index scan: %s", index, raw)
	}
}
