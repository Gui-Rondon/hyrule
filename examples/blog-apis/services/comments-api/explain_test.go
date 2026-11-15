package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/Gui-Rondon/hyrule/examples/blog-apis/internal/commentsschema"
	"github.com/Gui-Rondon/hyrule/internal/pgexplain"
	"github.com/Gui-Rondon/hyrule/platform/postgres"
	"github.com/Gui-Rondon/hyrule/rest"
	"github.com/Gui-Rondon/hyrule/restpostgres"
)

func TestDeclaredIndexExplain(t *testing.T) {
	if os.Getenv("POSTGRES_EXPLAIN_TEST") != "1" {
		t.Skip("manual local plans: make postgres-ha-explain-test")
	}
	runDeclaredIndexExplain(t, false)
}

func runDeclaredIndexExplain(t *testing.T, strict bool) {
	uri := os.Getenv("POSTGRES_EXPLAIN_URI")
	if uri == "" {
		t.Fatal("POSTGRES_EXPLAIN_URI required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	report := &pgexplain.Report{CreatedUTC: time.Now().UTC(), RowsSeeded: 20000}
	_, file, _, _ := runtime.Caller(0)
	dir := filepath.Join(filepath.Dir(file), "../../../..", ".local/postgres-explain", time.Now().UTC().Format("20060102T150405Z")+"-"+uuid.NewString())
	dir = filepath.Clean(dir)
	t.Logf("EXPLAIN artifacts: %s", dir)
	t.Cleanup(func() {
		if t.Failed() && len(report.Errors) == 0 {
			report.Errors = append(report.Errors, "test/setup/cleanup failed; see Go test output")
		}
		report.Complete = !t.Failed() && len(report.Errors) == 0 && len(report.Results) == len(report.Scenarios)*3 && len(report.Scenarios) > 0
		if err := report.Save(dir); err != nil {
			t.Errorf("save EXPLAIN report: %v", err)
		}
	})
	// Register report finalization before pool/schema cleanups, so it runs last
	// and cannot label a run complete when database cleanup failed.
	pool := gatePool(t, uri, "a6_", pgexplain.Tracer{})
	if err := pool.QueryRow(ctx, "SELECT version(), current_schema(), system_identifier::text FROM pg_control_system()").Scan(&report.ServerVersion, &report.Schema, &report.SystemID); err != nil {
		t.Fatal(err)
	}
	report.Settings = map[string]string{}
	for _, key := range []string{"enable_seqscan", "enable_indexscan", "enable_bitmapscan", "enable_indexonlyscan", "plan_cache_mode", "seq_page_cost", "random_page_cost", "work_mem", "default_statistics_target", "jit", "TimeZone"} {
		var value string
		if err := pool.QueryRow(ctx, "SELECT current_setting($1)", key).Scan(&value); err != nil {
			t.Fatal(err)
		}
		report.Settings[key] = value
		if strings.HasPrefix(key, "enable_") && value != "on" {
			t.Fatalf("unforced scan settings required: %s=%s", key, value)
		}
	}
	m := openMigration(t, pool)
	for _, v := range []uint{1, 2, 3} {
		if err := commentsschema.To(m, v); err != nil {
			t.Fatal(err)
		}
	}
	m.Close()
	if err := postgres.EnsureFunctions(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `CREATE TABLE explain_fixture (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(), doc jsonb NOT NULL DEFAULT '{}',
  created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(),
  version bigint NOT NULL DEFAULT 1, deleted boolean NOT NULL DEFAULT false
 )`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO comments(doc)
 SELECT jsonb_build_object('post_id', CASE WHEN i<=10000 THEN 'hot-post' ELSE 'post-'||i END,
 'author_name','Link','body','deterministic A6 comment','status','pending')
 FROM generate_series(1,20000) i`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO explain_fixture(doc)
 SELECT jsonb_build_object('category','cat-'||(i%100),'rank',i,
 'published_at',to_char(timestamptz '2020-01-01 00:00:00+00' + i*interval '1 second','YYYY-MM-DD"T"HH24:MI:SS"Z"'))
 || CASE WHEN i%4=0 THEN '{}'::jsonb ELSE jsonb_build_object('slug','slug-'||i) END
 FROM generate_series(1,20000) i`); err != nil {
		t.Fatal(err)
	}
	// Use the actual application's declarations. A new declaration must receive
	// its own case below; Inventory fails instead of silently ignoring it.
	declared := spec().PostgresIndexes
	fixtures := []postgres.IndexSpec{
		{Table: "explain_fixture", Name: "a6_unique_sparse_slug", Unique: true, Sparse: true, Fields: []postgres.IndexField{{Field: "slug"}}},
		{Table: "explain_fixture", Name: "a6_category_rank_desc", Fields: []postgres.IndexField{{Field: "category"}, {Field: "rank", Desc: true}}},
		{Table: "explain_fixture", Name: "a6_datetime", Fields: []postgres.IndexField{{Field: "published_at", Kind: postgres.KindDateTime}}},
	}
	all := append(append([]postgres.IndexSpec(nil), declared...), fixtures...)
	report.Declarations = all
	if len(declared) != 1 {
		t.Fatal("application index declarations changed; add EXPLAIN scenarios")
	}
	fixtureSchema := rest.Schema{"slug": {Type: rest.TypeString}, "category": {Type: rest.TypeString}, "rank": {Type: rest.TypeInteger}, "published_at": {Type: rest.TypeDatetime}}
	checkField := func(field string, value any) func([]rest.Doc) error {
		return func(docs []rest.Doc) error {
			for _, doc := range docs {
				if doc[field] != value {
					return fmt.Errorf("%s=%v want=%v", field, doc[field], value)
				}
			}
			return nil
		}
	}
	var primaryControlID string
	if err := pool.QueryRow(ctx, "SELECT id::text FROM explain_fixture LIMIT 1").Scan(&primaryControlID); err != nil {
		t.Fatal(err)
	}
	cases := []pgexplain.Case{
		{Name: "comments-selective", Index: declared[0], Schema: commentsSchema, Filter: postgres.Query{"post_id": "post-15001"}, WantRows: 1, Check: checkField("post_id", "post-15001")},
		{Name: "comments-common", Index: declared[0], Schema: commentsSchema, Filter: postgres.Query{"post_id": "hot-post"}, WantRows: 10000, Check: checkField("post_id", "hot-post")},
		{Name: "unique-sparse-present", Index: fixtures[0], Schema: fixtureSchema, Filter: postgres.Query{"slug": "slug-15001"}, WantRows: 1, Check: checkField("slug", "slug-15001")},
		{Name: "sparse-missing-control", Index: fixtures[0], Schema: fixtureSchema, Filter: postgres.Query{"slug": postgres.Query{"$exists": false}}, WantRows: 5000, Check: func(docs []rest.Doc) error {
			for _, doc := range docs {
				if _, ok := doc["slug"]; ok {
					return fmt.Errorf("missing-key filter returned slug")
				}
			}
			return nil
		}},
		{Name: "compound-desc", Index: fixtures[1], Schema: fixtureSchema, Filter: postgres.Query{"category": "cat-7"}, Sort: []restpostgres.SortTerm{{Field: "category"}, {Field: "rank", Desc: true}}, Limit: 10, WantRows: 10, Check: func(docs []rest.Doc) error {
			for i, doc := range docs {
				rank, err := strconv.Atoi(fmt.Sprint(doc["rank"]))
				if err != nil || rank != 19907-i*100 || doc["category"] != "cat-7" {
					return fmt.Errorf("compound ordering/result mismatch: %v", doc)
				}
			}
			return nil
		}},
		{Name: "primary-key-lookup-control", Index: fixtures[1], Schema: fixtureSchema, Filter: uuid.MustParse(primaryControlID), WantRows: 1, Check: checkField("_id", uuid.MustParse(primaryControlID))},
		{Name: "datetime-selective", Index: fixtures[2], Schema: fixtureSchema, Filter: postgres.Query{"published_at": postgres.Query{"$gt": "2020-01-01T05:33:10Z"}}, WantRows: 10, Check: func(docs []rest.Doc) error {
			for _, doc := range docs {
				value, err := time.Parse(time.RFC3339, fmt.Sprint(doc["published_at"]))
				boundary := time.Date(2020, 1, 1, 5, 33, 10, 0, time.UTC)
				if err != nil || !value.After(boundary) || value.After(boundary.Add(10*time.Second)) {
					return fmt.Errorf("unexpected datetime %v", doc["published_at"])
				}
			}
			return nil
		}},
		{Name: "datetime-common", Index: fixtures[2], Schema: fixtureSchema, Filter: postgres.Query{"published_at": postgres.Query{"$gte": "2020-01-01T00:00:00Z"}}, WantRows: 20000},
	}
	cases = append(cases, pgexplain.Case{Name: "comments-selective-after-hot", Index: declared[0], Schema: commentsSchema, Filter: postgres.Query{"post_id": "post-15001"}, WantRows: 1, Check: checkField("post_id", "post-15001"), WarmupFilter: postgres.Query{"post_id": "hot-post"}, WantWarmupRows: 10000})
	if err := pgexplain.Inventory(all, cases); err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		report.Scenarios = append(report.Scenarios, c.Name)
	}
	if _, err := postgres.EnsureIndexes(ctx, pool, all, postgres.IndexesCreate); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"comments", "explain_fixture"} {
		if _, err := pool.Exec(ctx, "ANALYZE "+table); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range cases {
		if err := report.Run(ctx, pool, c); err != nil {
			t.Fatalf("EXPLAIN %s: %v", c.Name, err)
		}
		for _, result := range report.Results[len(report.Results)-3:] {
			if strict && (c.Name == "comments-selective" || c.Name == "comments-selective-after-hot" || c.Name == "datetime-selective") && (result.Mode == "auto" || result.Mode == "force_custom_plan") {
				requireSelectiveIndexCondition(t, result.Plan, result.ExpectedIndex)
				report.RegressionChecks = append(report.RegressionChecks, c.Name+"/"+result.Mode+"/index_cond")
			}
			t.Logf("%s %s: %s expected=%s used=%v scans=%v rows=%d buffers=%d/%d time=%.3fms custom/generic=%d/%d", c.Name, result.Mode, result.Status, result.ExpectedIndex, result.UsedIndexes, result.ScanTypes, result.Rows, result.SharedHits, result.SharedReads, result.ExecutionMS, result.CustomPlans, result.GenericPlans)
		}
	}
	if strict {
		checkDatetimeEvaluations(t, ctx, pool, report.RowsSeeded)
		report.RegressionChecks = append(report.RegressionChecks, "datetime_generic_single_evaluation")
	}
	// Guard controls happen only after collecting all real plans, on our own
	// fixture indexes. Each failed observation uses its own partial report.
	if _, err := pool.Exec(ctx, "DROP INDEX a6_unique_sparse_slug"); err != nil {
		t.Fatal(err)
	}
	negative := &pgexplain.Report{}
	if err := negative.Run(ctx, pool, cases[2]); err == nil || !strings.Contains(err.Error(), "index catalog") {
		t.Fatalf("missing index not rejected: %v", err)
	}
	report.NegativeControls = append(report.NegativeControls, "missing_index_rejected")
	_, err := pool.Exec(ctx, "CREATE UNIQUE INDEX CONCURRENTLY a6_invalid_control ON explain_fixture ((doc->'category'))")
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23505" {
		t.Fatalf("invalid-index setup expected unique violation: %v", err)
	}
	invalid := cases[2]
	invalid.Name = "invalid-index-control"
	invalid.Index = postgres.IndexSpec{Table: "explain_fixture", Name: "a6_invalid_control", Unique: true, Fields: []postgres.IndexField{{Field: "category"}}}
	if err := negative.Run(ctx, pool, invalid); err == nil || !strings.Contains(err.Error(), "invalid/not ready") {
		t.Fatalf("invalid index not rejected: %v", err)
	}
	report.NegativeControls = append(report.NegativeControls, "invalid_index_rejected")
	t.Logf("catalog guard controls: %v", report.NegativeControls)

}
