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

	"github.com/Gui-Rondon/hyrule/internal/pgexplain"
	"github.com/Gui-Rondon/hyrule/platform/postgres"
	"github.com/Gui-Rondon/hyrule/rest"
	"github.com/Gui-Rondon/hyrule/restpostgres"
)

// Opt-in only: real EnsureIndexes, real Find, unforced prepared plans.
func TestTypedSortPlans(t *testing.T) {
	if os.Getenv("POSTGRES_TYPED_SORT_TEST") != "1" {
		t.Skip("manual typed ordering: make postgres-ha-typed-sort-test")
	}
	uri := os.Getenv("POSTGRES_EXPLAIN_URI")
	if uri == "" {
		t.Fatal("POSTGRES_EXPLAIN_URI required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	report := &pgexplain.Report{CreatedUTC: time.Now().UTC(), RowsSeeded: 20000, Settings: map[string]string{}}
	_, file, _, _ := runtime.Caller(0)
	dir := filepath.Join(filepath.Dir(file), "../../../..", ".local/postgres-typed-sort", time.Now().UTC().Format("20060102T150405Z")+"-"+uuid.NewString())
	t.Logf("typed sort artifacts: %s", dir)
	t.Cleanup(func() {
		if !t.Failed() && (len(report.Results) != 48 || len(report.RegressionChecks) != 24) {
			t.Error("incomplete typed sort matrix")
		}
		if t.Failed() {
			report.Errors = append(report.Errors, "test/setup/cleanup failed; see Go test output")
		}
		report.Complete = !t.Failed() && len(report.Results) == 48 && len(report.RegressionChecks) == 24
		if err := report.Save(dir); err != nil {
			t.Error(err)
		}
	})
	pool := gatePool(t, uri, "typedsort_", pgexplain.Tracer{})
	if err := pool.QueryRow(ctx, "SELECT version(),current_schema(),system_identifier::text FROM pg_control_system()").Scan(&report.ServerVersion, &report.Schema, &report.SystemID); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"enable_seqscan", "enable_indexscan", "enable_bitmapscan", "enable_indexonlyscan", "plan_cache_mode", "TimeZone"} {
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
	exec := func(sql string) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql); err != nil {
			t.Fatal(err)
		}
	}
	exec(`CREATE TABLE typed_fixture (id uuid PRIMARY KEY, doc jsonb NOT NULL, created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(), version bigint NOT NULL DEFAULT 1, deleted boolean NOT NULL DEFAULT false)`)
	exec(`INSERT INTO typed_fixture(id,doc) SELECT md5(i::text)::uuid,
 jsonb_build_object('tie',i,'label',(ARRAY['á','ação','Z','ê','ábaco'])[i%5+1]||lpad(i::text,6,'0'),'integer',i-10001,'number',(i-10001)::numeric/7,'boolean',i%2=0,'group','all')
 - CASE WHEN i%101=0 THEN 'label' ELSE 'unused' END
 - CASE WHEN i%101=0 THEN 'integer' ELSE 'unused' END
 - CASE WHEN i%101=0 THEN 'number' ELSE 'unused' END
 - CASE WHEN i%101=0 THEN 'boolean' ELSE 'unused' END
 || CASE WHEN i%103=0 THEN '{"label":null,"integer":null,"number":null,"boolean":null}'::jsonb ELSE '{}'::jsonb END
 FROM generate_series(20000,1,-1) i`)
	exec("ANALYZE typed_fixture")
	schema := rest.Schema{"label": {Type: rest.TypeString}, "integer": {Type: rest.TypeInteger}, "number": {Type: rest.TypeNumber}, "boolean": {Type: rest.TypeBoolean}, "tie": {Type: rest.TypeInteger}, "group": {Type: rest.TypeString}}
	adapter, err := restpostgres.NewAdapter(postgres.NewStore(pool, "typed_fixture"), postgres.Transactor{Pool: pool}, restpostgres.Options{Schema: schema, Retry: rest.DefaultRetryOptions()})
	if err != nil {
		t.Fatal(err)
	}
	type scenario struct {
		c       pgexplain.Case
		ids     []string
		require bool
		desc    bool
	}
	var scenarios []scenario
	fullBaseline := map[string][]string{}
	ids := func(docs []rest.Doc) []string {
		out := make([]string, len(docs))
		for i, d := range docs {
			out[i] = fmt.Sprint(d["_id"])
		}
		return out
	}
	for _, f := range []struct {
		name string
		kind postgres.Kind
	}{{"label", postgres.KindString}, {"integer", postgres.KindInteger}, {"number", postgres.KindNumber}, {"boolean", postgres.KindBoolean}, {"tie", postgres.KindInteger}} {
		fields := []postgres.IndexField{{Field: f.name, Kind: f.kind, Mode: postgres.IndexFieldTypedSort}}
		if f.name != "tie" {
			fields = append(fields, postgres.IndexField{Field: "tie", Kind: postgres.KindInteger, Mode: postgres.IndexFieldTypedSort, Desc: f.name == "number"})
		}
		spec := postgres.IndexSpec{Table: "typed_fixture", Name: "typed_" + f.name, Fields: fields}
		report.Declarations = append(report.Declarations, spec)
		legacy := postgres.IndexSpec{Table: "typed_fixture", Name: "legacy_" + f.name, Fields: []postgres.IndexField{{Field: f.name, Kind: f.kind}}}
		if _, err := postgres.EnsureIndexes(ctx, pool, []postgres.IndexSpec{legacy}, postgres.IndexesCreate); err != nil {
			t.Fatal(err)
		}
		for _, desc := range []bool{false, true} {
			sort := []restpostgres.SortTerm{{Field: f.name, Desc: desc}}
			if f.name != "tie" {
				sort = append(sort, restpostgres.SortTerm{Field: "tie", Desc: desc != (f.name == "number")})
			}
			skips := []int64{0}
			if f.name == "integer" {
				skips = []int64{0, 10, 19990}
			}
			for _, skip := range skips {
				c := pgexplain.Case{Name: fmt.Sprintf("%s-desc-%t-offset-%d", f.name, desc, skip), Index: spec, Schema: schema, Filter: postgres.Query{"group": "all"}, Sort: sort, Skip: skip, Limit: 10, WantRows: 10}
				docs, err := adapter.Find(ctx, c.Filter, c.Sort, nil, c.Skip, c.Limit)
				if err != nil {
					t.Fatal(err)
				}
				if skip == 0 {
					all, err := adapter.Find(ctx, c.Filter, c.Sort, nil, 0, 20000)
					if err != nil || len(all) != 20000 {
						t.Fatalf("full baseline: %d / %v", len(all), err)
					}
					fullBaseline[c.Name] = ids(all)
				}
				scenarios = append(scenarios, scenario{c, ids(docs), skip < 19990, desc})
			}
		}
	}
	// A JSONB filter is deliberately unaligned with the typed numeric expression.
	for _, desc := range []bool{false, true} {
		c := pgexplain.Case{Name: fmt.Sprintf("unaligned-filter-%t", desc), Index: report.Declarations[1], Schema: schema, Filter: postgres.Query{"integer": postgres.Query{"$gt": 9990}}, Sort: []restpostgres.SortTerm{{Field: "integer", Desc: desc}, {Field: "tie", Desc: desc}}, Limit: 5, WantRows: 5}
		docs, err := adapter.Find(ctx, c.Filter, c.Sort, nil, 0, 5)
		if err != nil {
			t.Fatal(err)
		}
		scenarios = append(scenarios, scenario{c, ids(docs), false, desc})
	}
	for _, spec := range report.Declarations {
		if _, err := postgres.EnsureIndexes(ctx, pool, []postgres.IndexSpec{spec}, postgres.IndexesCreate); err != nil {
			t.Fatal(err)
		}
	}
	exec("ANALYZE typed_fixture")
	for _, s := range scenarios {
		c := s.c
		if want, ok := fullBaseline[c.Name]; ok {
			all, err := adapter.Find(ctx, c.Filter, c.Sort, nil, 0, 20000)
			if err != nil || !reflect.DeepEqual(ids(all), want) {
				t.Fatalf("full ordered corpus changed for %s: %v", c.Name, err)
			}
		}
		c.Check = func(docs []rest.Doc) error {
			if !reflect.DeepEqual(ids(docs), s.ids) {
				return fmt.Errorf("ordered IDs changed after index creation")
			}
			return nil
		}
		report.Scenarios = append(report.Scenarios, c.Name)
		if err := report.Run(ctx, pool, c); err != nil {
			t.Fatal(err)
		}
		for _, r := range report.Results[len(report.Results)-3:] {
			if s.require && r.Mode != "force_generic_plan" {
				requireIndexOrder(t, r.Plan, c.Index.Name, s.desc)
				report.RegressionChecks = append(report.RegressionChecks, c.Name+"/"+r.Mode+"/index_without_sort")
			}
			t.Logf("%s %s: %v hits=%d", c.Name, r.Mode, r.UsedIndexes, r.SharedHits)
		}
	}
	// Invalid preexisting values reject creation; invalid future writes reject too.
	for _, f := range []struct {
		name string
		kind postgres.Kind
	}{{"integer", postgres.KindInteger}, {"number", postgres.KindNumber}, {"boolean", postgres.KindBoolean}} {
		t.Logf("invalid cast creation/write control: %s", f.name)
		exec(`CREATE TABLE invalid_fixture (doc jsonb NOT NULL)`)
		exec(`INSERT INTO invalid_fixture VALUES ('{"v":"invalid"}')`)
		spec := postgres.IndexSpec{Table: "invalid_fixture", Name: "invalid_typed", Fields: []postgres.IndexField{{Field: "v", Kind: f.kind, Mode: postgres.IndexFieldTypedSort}}}
		_, err := postgres.EnsureIndexes(ctx, pool, []postgres.IndexSpec{spec}, postgres.IndexesCreate)
		requireCastError(t, err)
		var exists bool
		if err := pool.QueryRow(ctx, "SELECT to_regclass('invalid_typed') IS NOT NULL").Scan(&exists); err != nil || exists {
			t.Fatalf("failed index left artifact: %t / %v", exists, err)
		}
		exec("TRUNCATE invalid_fixture")
		if _, err := postgres.EnsureIndexes(ctx, pool, []postgres.IndexSpec{spec}, postgres.IndexesCreate); err != nil {
			t.Fatal(err)
		}
		_, err = pool.Exec(ctx, `INSERT INTO invalid_fixture VALUES ('{"v":"invalid"}')`)
		requireCastError(t, err)
		exec("DROP TABLE invalid_fixture")
	}
}

func requireCastError(t *testing.T, err error) {
	t.Helper()
	var pe *pgconn.PgError
	if !errors.As(err, &pe) || pe.Code != "22P02" {
		t.Fatalf("expected invalid cast 22P02, got %v", err)
	}
}
