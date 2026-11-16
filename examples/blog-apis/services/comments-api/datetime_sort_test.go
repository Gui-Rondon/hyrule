package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Gui-Rondon/hyrule/internal/pgexplain"
	"github.com/Gui-Rondon/hyrule/platform/postgres"
	"github.com/Gui-Rondon/hyrule/rest"
	"github.com/Gui-Rondon/hyrule/restpostgres"
)

func TestDateTimeSortPlans(t *testing.T) {
	if os.Getenv("POSTGRES_DATETIME_SORT_TEST") != "1" {
		t.Skip("manual opt-in datetime ordering: make postgres-ha-datetime-sort-test")
	}
	uri := os.Getenv("POSTGRES_EXPLAIN_URI")
	if uri == "" {
		t.Fatal("POSTGRES_EXPLAIN_URI required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	report := &pgexplain.Report{CreatedUTC: time.Now().UTC(), RowsSeeded: 20000, Settings: map[string]string{}}
	_, file, _, _ := runtime.Caller(0)
	dir := filepath.Clean(filepath.Join(filepath.Dir(file), "../../../..", ".local/postgres-datetime-sort", time.Now().UTC().Format("20060102T150405Z")+"-"+uuid.NewString()))
	t.Logf("datetime sort artifacts: %s", dir)
	t.Cleanup(func() {
		if !t.Failed() && (len(report.Results) != 48 || len(report.RegressionChecks) != 24) {
			t.Error("incomplete datetime matrix")
		}
		report.Complete = !t.Failed()
		if t.Failed() {
			report.Errors = append(report.Errors, "test/setup/cleanup failed; see Go test output")
		}
		if err := report.Save(dir); err != nil {
			t.Error(err)
		}
	})
	root := gatePool(t, uri, "datesort_", pgexplain.Tracer{})
	if err := root.QueryRow(ctx, "SELECT version(),current_schema(),system_identifier::text FROM pg_control_system()").Scan(&report.ServerVersion, &report.Schema, &report.SystemID); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"enable_seqscan", "enable_indexscan", "enable_bitmapscan", "enable_indexonlyscan", "plan_cache_mode"} {
		var value string
		if err := root.QueryRow(ctx, "SELECT current_setting($1)", key).Scan(&value); err != nil {
			t.Fatal(err)
		}
		report.Settings[key] = value
		if strings.HasPrefix(key, "enable_") && value != "on" {
			t.Fatalf("unforced planner required: %s=%s", key, value)
		}
	}
	exec := func(sql string) {
		t.Helper()
		if _, err := root.Exec(ctx, sql); err != nil {
			t.Fatal(err)
		}
	}
	exec(`CREATE TABLE fixture(id uuid PRIMARY KEY,doc jsonb NOT NULL,created_at timestamptz NOT NULL DEFAULT now(),updated_at timestamptz NOT NULL DEFAULT now(),version bigint NOT NULL DEFAULT 1,deleted boolean NOT NULL DEFAULT false)`)
	exec(`INSERT INTO fixture(id,doc) SELECT md5(i::text)::uuid,jsonb_build_object('when',CASE WHEN i%2=0 THEN to_char(timestamp '2020-01-01 00:00:00'+i*interval '1 second','YYYY-MM-DD"T"HH24:MI:SS"Z"') ELSE to_char(timestamp '2019-12-31 21:00:00'+i*interval '1 second','YYYY-MM-DD"T"HH24:MI:SS')||'-03:00' END,'tie',i,'group','all') FROM generate_series(1,20000)i`)
	if err := postgres.EnsureFunctions(ctx, root); err != nil {
		t.Fatal(err)
	}
	spec := postgres.IndexSpec{Table: "fixture", Name: "date_helper_tie", Fields: []postgres.IndexField{{Field: "when", Kind: postgres.KindDateTime}, {Field: "tie", Kind: postgres.KindInteger, Mode: postgres.IndexFieldTypedSort}}}
	if _, err := postgres.EnsureIndexes(ctx, root, []postgres.IndexSpec{spec}, postgres.IndexesCreate); err != nil {
		t.Fatal(err)
	}
	report.Declarations = []postgres.IndexSpec{spec}
	exec("ANALYZE fixture")
	schema := rest.Schema{"when": {Type: rest.TypeDatetime}, "tie": {Type: rest.TypeInteger}, "group": {Type: rest.TypeString}}
	for _, mixed := range []bool{false, true} {
		if mixed {
			// Repeated valid instants plus NULL groups test explicit typed tie ordering.
			exec(`UPDATE fixture SET doc=jsonb_set(doc,'{when}',CASE WHEN (doc->>'tie')::int%101=0 THEN 'null'::jsonb WHEN (doc->>'tie')::int%103=0 THEN '"bad"'::jsonb WHEN (doc->>'tie')::int%107=0 THEN '"2020-01-01"'::jsonb ELSE to_jsonb('2020-01-01T00:00:00Z'::text) END) WHERE (doc->>'tie')::int%5=0`)
			exec(`UPDATE fixture SET doc=doc-'when' WHERE (doc->>'tie')::int%109=0`)
			exec("ANALYZE fixture")
		}
		for _, zone := range []string{"UTC", "America/Sao_Paulo"} {
			cfg := root.Config()
			cfg.MaxConns = 1
			cfg.MinConns = 0
			cfg.ConnConfig.RuntimeParams["TimeZone"] = zone
			pool, err := pgxpool.NewWithConfig(ctx, cfg)
			if err != nil {
				t.Fatal(err)
			}
			func() {
				defer pool.Close()
				strict, err := restpostgres.NewAdapter(postgres.NewStore(pool, "fixture"), postgres.Transactor{Pool: pool}, restpostgres.Options{Schema: schema, Retry: rest.DefaultRetryOptions()})
				if err != nil {
					t.Fatal(err)
				}
				for _, desc := range []bool{false, true} {
					terms := []restpostgres.SortTerm{{Field: "when", Desc: desc}}
					if mixed {
						terms = append(terms, restpostgres.SortTerm{Field: "tie", Desc: desc})
					}
					baseline, err := strict.Find(ctx, postgres.Query{"group": "all"}, terms, nil, 0, 20000)
					if mixed {
						var pe *pgconn.PgError
						if !errors.As(err, &pe) || pe.Code != "22007" {
							t.Fatalf("strict default must fail: %v", err)
						}
					} else if err != nil || len(baseline) != 20000 {
						t.Fatalf("baseline: %v", err)
					}
					dir := "ASC"
					if desc {
						dir = "DESC"
					}
					reference := "SELECT id::text FROM fixture ORDER BY hyrule_safe_timestamptz(doc->>'when') " + dir
					if mixed {
						reference += ", (doc->>'tie')::int8 " + dir
					}
					rows, err := pool.Query(ctx, reference)
					if err != nil {
						t.Fatal(err)
					}
					var want []string
					for rows.Next() {
						var id string
						if err := rows.Scan(&id); err != nil {
							rows.Close()
							t.Fatal(err)
						}
						want = append(want, id)
					}
					rows.Close()
					if err := rows.Err(); err != nil {
						t.Fatal(err)
					}
					if len(want) != 20000 {
						t.Fatal("reference corpus incomplete")
					}
					if !mixed {
						for i, d := range baseline {
							if fmt.Sprint(d["_id"]) != want[i] {
								t.Fatal("strict/helper valid order differs")
							}
						}
					}
					safe, err := restpostgres.NewAdapter(postgres.NewStore(pool, "fixture"), postgres.Transactor{Pool: pool}, restpostgres.Options{Schema: schema, Retry: rest.DefaultRetryOptions(), SafeDateTimeSort: true})
					if err != nil {
						t.Fatal(err)
					}
					all, err := safe.Find(ctx, postgres.Query{"group": "all"}, terms, nil, 0, 20000)
					if err != nil || len(all) != 20000 {
						t.Fatalf("safe corpus: %v", err)
					}
					got := make([]string, len(all))
					for i, d := range all {
						got[i] = fmt.Sprint(d["_id"])
					}
					if !reflect.DeepEqual(got, want) {
						t.Fatal("safe full corpus differs from helper reference")
					}
					skips := []int64{0}
					if mixed {
						skips = []int64{0, 10, 19990}
					}
					for _, skip := range skips {
						name := fmt.Sprintf("mixed-%t-%s-desc-%t-offset-%d", mixed, zone, desc, skip)
						c := pgexplain.Case{Name: name, Index: spec, Schema: schema, Filter: postgres.Query{"group": "all"}, Sort: terms, Skip: skip, Limit: 10, WantRows: 10, SafeDateTimeSort: true, Check: func(docs []rest.Doc) error {
							for i, d := range docs {
								if fmt.Sprint(d["_id"]) != want[int(skip)+i] {
									return fmt.Errorf("page order differs")
								}
							}
							return nil
						}}
						report.Scenarios = append(report.Scenarios, name)
						if err := report.Run(ctx, pool, c); err != nil {
							t.Fatal(err)
						}
						for _, r := range report.Results[len(report.Results)-3:] {
							if skip < 19990 && r.Mode != "force_generic_plan" {
								requireIndexOrder(t, r.Plan, spec.Name, desc)
								report.RegressionChecks = append(report.RegressionChecks, name+"/"+r.Mode+"/index_without_sort")
							}
							t.Logf("%s %s: %v hits=%d", name, r.Mode, r.UsedIndexes, r.SharedHits)
						}
					}
				}
			}()
		}
	}
}
