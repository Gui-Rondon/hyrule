package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Gui-Rondon/hyrule/internal/pgexplain"
	"github.com/Gui-Rondon/hyrule/platform/config"
	"github.com/Gui-Rondon/hyrule/platform/postgres"
	"github.com/Gui-Rondon/hyrule/rest"
	"github.com/Gui-Rondon/hyrule/restpostgres"
)

type policyCaptureKey struct{}
type policyCapture struct {
	SQL   string
	Args  []any
	Count int
}
type policyTracer struct{}

func (policyTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, d pgx.TraceQueryStartData) context.Context {
	if c, ok := ctx.Value(policyCaptureKey{}).(*policyCapture); ok {
		c.Count++
		c.SQL = d.SQL
		c.Args = append([]any(nil), d.Args...)
	}
	return ctx
}
func (policyTracer) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

type policyResult struct {
	Case       string          `json:"case"`
	Policy     string          `json:"policy"`
	SQL        string          `json:"sql"`
	Args       []any           `json:"args"`
	WarmupArgs []any           `json:"warmup_args"`
	WarmupRows int             `json:"warmup_rows"`
	Rows       int             `json:"rows"`
	Hash       string          `json:"result_sha256"`
	Custom     int64           `json:"custom_after_adapter"`
	Generic    int64           `json:"generic_after_adapter"`
	ActualPlan string          `json:"explained_plan_kind"`
	Indexes    []string        `json:"used_indexes"`
	Plan       json.RawMessage `json:"plan"`
}

// TestPlanPolicy uses the driver's actual cached Find statement, not a separate
// PREPARE of equivalent SQL. All pools and settings belong to this local gate.
func TestPlanPolicy(t *testing.T) {
	if os.Getenv("POSTGRES_PLAN_POLICY_TEST") != "1" {
		t.Skip("manual plan policy: make postgres-ha-plan-policy-test")
	}
	uri := os.Getenv("POSTGRES_EXPLAIN_URI")
	if uri == "" {
		t.Fatal("POSTGRES_EXPLAIN_URI required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	report := struct {
		Complete       bool              `json:"complete"`
		Created        time.Time         `json:"created_utc"`
		Server         string            `json:"server_version"`
		Schema         string            `json:"schema"`
		Settings       map[string]string `json:"settings"`
		Rows           int               `json:"rows_seeded"`
		URIConnections []string          `json:"uri_connection_policies"`
		Results        []policyResult    `json:"results"`
		Errors         []string          `json:"errors,omitempty"`
	}{Created: time.Now().UTC(), Rows: 20000, Settings: map[string]string{}}
	_, file, _, _ := runtime.Caller(0)
	dir := filepath.Clean(filepath.Join(filepath.Dir(file), "../../../..", ".local/postgres-plan-policy", time.Now().UTC().Format("20060102T150405Z")+"-"+uuid.NewString()))
	t.Logf("plan policy artifacts: %s", dir)
	t.Cleanup(func() {
		if !t.Failed() && (len(report.Results) != 12 || len(report.URIConnections) != 2) {
			t.Error("incomplete policy matrix")
		}
		report.Complete = !t.Failed()
		if t.Failed() {
			report.Errors = append(report.Errors, "test/setup/cleanup failed; see Go test output")
		}
		raw, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			t.Error(err)
			return
		}
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Error(err)
			return
		}
		temp := filepath.Join(dir, "report.json.tmp")
		defer os.Remove(temp)
		if err := os.WriteFile(temp, raw, 0644); err != nil {
			t.Error(err)
			return
		}
		if err := os.Rename(temp, filepath.Join(dir, "report.json")); err != nil {
			t.Error(err)
		}
	})
	root := gatePool(t, uri, "policy_", nil)
	if err := root.QueryRow(ctx, "SELECT version(), current_schema()").Scan(&report.Server, &report.Schema); err != nil {
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
	exec(`INSERT INTO fixture(id,doc) SELECT md5(i::text)::uuid,jsonb_build_object('post',CASE WHEN i<=10000 THEN 'hot' ELSE 'post-'||i END,'rank',i,'unindexed',20001-i,'date',to_char(timestamptz '2020-01-01'+i*interval '1 second','YYYY-MM-DD"T"HH24:MI:SS"Z"')) FROM generate_series(1,20000)i`)
	if err := postgres.EnsureFunctions(ctx, root); err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.EnsureIndexes(ctx, root, []postgres.IndexSpec{
		{Table: "fixture", Name: "policy_post", Fields: []postgres.IndexField{{Field: "post"}}},
		{Table: "fixture", Name: "policy_date", Fields: []postgres.IndexField{{Field: "date", Kind: postgres.KindDateTime}}},
		{Table: "fixture", Name: "policy_rank", Fields: []postgres.IndexField{{Field: "rank", Kind: postgres.KindInteger, Mode: postgres.IndexFieldTypedSort}}},
	}, postgres.IndexesCreate); err != nil {
		t.Fatal(err)
	}
	exec("ANALYZE fixture")
	var pk string
	if err := root.QueryRow(ctx, `SELECT indexrelid::regclass::text FROM pg_index WHERE indrelid='fixture'::regclass AND indisprimary`).Scan(&pk); err != nil {
		t.Fatal(err)
	}
	works := []struct {
		name           string
		warm, filter   any
		warmSort, sort []restpostgres.SortTerm
		limit          int64
		warmRows, want int
		index          string
	}{
		{name: "equality-hot-to-rare", warm: postgres.Query{"post": "hot"}, filter: postgres.Query{"post": "post-15001"}, warmRows: 10000, want: 1, index: "policy_post"},
		{name: "datetime-wide-to-selective", warm: postgres.Query{"date": postgres.Query{"$gt": "2020-01-01T00:00:00Z"}}, filter: postgres.Query{"date": postgres.Query{"$gt": "2020-01-01T05:33:10Z"}}, warmRows: 20000, want: 10, index: "policy_date"},
		{name: "sort-unindexed-to-indexed", warmSort: []restpostgres.SortTerm{{Field: "unindexed"}}, sort: []restpostgres.SortTerm{{Field: "rank"}}, limit: 10, warmRows: 10, want: 10, index: "policy_rank"},
		{name: "uuid-control", warmSort: []restpostgres.SortTerm{{Field: "_id"}}, sort: []restpostgres.SortTerm{{Field: "_id"}}, limit: 10, warmRows: 10, want: 10, index: pk},
	}
	hashes := map[string]string{}
	for _, policy := range []string{"auto", "force_custom_plan", "force_generic_plan"} {
		for _, w := range works {
			t.Run(w.name+"/"+policy, func(t *testing.T) {
				cfg := root.Config()
				cfg.MaxConns = 1
				cfg.MinConns = 0
				cfg.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeCacheStatement
				cfg.ConnConfig.RuntimeParams["plan_cache_mode"] = policy
				cfg.ConnConfig.Tracer = policyTracer{}
				pool, err := pgxpool.NewWithConfig(ctx, cfg)
				if err != nil {
					t.Fatal(err)
				}
				defer pool.Close()
				adapter, err := restpostgres.NewAdapter(postgres.NewStore(pool, "fixture"), postgres.Transactor{Pool: pool}, restpostgres.Options{Schema: rest.Schema{"post": {Type: rest.TypeString}, "rank": {Type: rest.TypeInteger}, "unindexed": {Type: rest.TypeInteger}, "date": {Type: rest.TypeDatetime}}, Retry: rest.DefaultRetryOptions()})
				if err != nil {
					t.Fatal(err)
				}
				var warm policyCapture
				for range 5 {
					warm = policyCapture{}
					docs, err := adapter.Find(context.WithValue(ctx, policyCaptureKey{}, &warm), w.warm, w.warmSort, nil, 0, w.limit)
					if err != nil || len(docs) != w.warmRows || warm.Count != 1 {
						t.Fatalf("warmup: rows=%d captures=%d err=%v", len(docs), warm.Count, err)
					}
				}
				cap := &policyCapture{}
				docs, err := adapter.Find(context.WithValue(ctx, policyCaptureKey{}, cap), w.filter, w.sort, nil, 0, w.limit)
				if err != nil || len(docs) != w.want || cap.Count != 1 || cap.SQL != warm.SQL {
					t.Fatalf("adapter result/shape: rows=%d captures=%d err=%v", len(docs), cap.Count, err)
				}
				hash := policyResultHash(docs, len(w.sort) > 0)
				if want, ok := hashes[w.name]; ok && want != hash {
					t.Fatal("ordered/result IDs changed across policies")
				}
				hashes[w.name] = hash
				r := policyResult{Case: w.name, Policy: policy, SQL: cap.SQL, Args: cap.Args, WarmupArgs: warm.Args, WarmupRows: w.warmRows, Rows: len(docs), Hash: hash}
				var name string
				if err := pool.QueryRow(ctx, "SELECT name,custom_plans,generic_plans FROM pg_prepared_statements WHERE statement=$1", cap.SQL).Scan(&name, &r.Custom, &r.Generic); err != nil {
					t.Fatal(err)
				}
				if r.Custom+r.Generic != 6 {
					t.Fatalf("actual cached Find executions=%d", r.Custom+r.Generic)
				}
				slots := make([]string, len(cap.Args))
				for i := range slots {
					slots[i] = fmt.Sprintf("$%d", i+1)
				}
				execute := "EXECUTE " + pgx.Identifier{name}.Sanitize()
				if len(slots) > 0 {
					execute += "(" + strings.Join(slots, ",") + ")"
				}
				args := append([]any{pgx.QueryExecModeSimpleProtocol}, cap.Args...)
				if err := pool.QueryRow(ctx, "EXPLAIN (ANALYZE,BUFFERS,SETTINGS,FORMAT JSON) "+execute, args...).Scan(&r.Plan); err != nil {
					t.Fatal(err)
				}
				var custom, generic int64
				if err := pool.QueryRow(ctx, "SELECT custom_plans,generic_plans FROM pg_prepared_statements WHERE name=$1", name).Scan(&custom, &generic); err != nil {
					t.Fatal(err)
				}
				switch {
				case custom-r.Custom == 1 && generic == r.Generic:
					r.ActualPlan = "custom"
				case generic-r.Generic == 1 && custom == r.Custom:
					r.ActualPlan = "generic"
				default:
					t.Fatal("unidentified explained plan")
				}
				var plans []struct{ Plan pgexplain.Node }
				if err := json.Unmarshal(r.Plan, &plans); err != nil || len(plans) != 1 {
					t.Fatalf("invalid plan: %v", err)
				}
				r.Indexes, _ = pgexplain.Collect(plans[0].Plan)
				report.Results = append(report.Results, r)
				if policy == "force_custom_plan" && len(cap.Args) > 0 {
					if r.Custom != 6 || r.Generic != 0 || r.ActualPlan != "custom" {
						t.Fatal("custom policy not exercised")
					}
					if len(w.sort) > 0 {
						requireIndexOrder(t, r.Plan, w.index, false)
					} else {
						requireSelectiveIndexCondition(t, r.Plan, w.index)
					}
				}
				if policy == "force_generic_plan" && (r.Generic != 6 || r.Custom != 0 || r.ActualPlan != "generic") {
					t.Fatal("generic policy not exercised")
				}
				if w.name == "uuid-control" {
					requireIndexOrder(t, r.Plan, pk, false)
					if r.Generic != 6 || r.ActualPlan != "generic" {
						t.Fatal("parameterless statement should remain generic")
					}
				}
				// EXPLAIN executes the actual cached statement again. Read the same EXECUTE
				// through pgx's safely escaped utility-command arguments and verify its IDs.
				rows, err := pool.Query(ctx, execute, args...)
				if err != nil {
					t.Fatal(err)
				}
				var ids []string
				for rows.Next() {
					values, err := rows.Values()
					if err != nil {
						rows.Close()
						t.Fatal(err)
					}
					id, ok := values[0].(string)
					if !ok {
						rows.Close()
						t.Fatalf("id is %T", values[0])
					}
					ids = append(ids, id)
				}
				rows.Close()
				if err := rows.Err(); err != nil {
					t.Fatal(err)
				}
				if policyIDsHash(ids, len(w.sort) > 0) != hash {
					t.Fatal("EXECUTE result differs from actual adapter")
				}
				t.Logf("%s: actual=%s custom/generic=%d/%d indexes=%v", policy, r.ActualPlan, r.Custom, r.Generic, r.Indexes)
			})
		}
	}
	// Verify existing public Connect/URI support on two simultaneous connections.
	t.Setenv("POSTGRES_CONNECTION_STRING", "")
	parsed, err := url.Parse(uri)
	if err != nil {
		t.Fatal(err)
	}
	q := parsed.Query()
	q.Set("plan_cache_mode", "force_custom_plan")
	parsed.RawQuery = q.Encode()
	connected, err := postgres.Connect(ctx, config.Config{"postgres": map[string]any{"uri": parsed.String(), "max_conns": "2"}}, "postgres")
	if err != nil {
		t.Fatal(err)
	}
	defer connected.Close()
	for range 2 {
		conn, err := connected.Acquire(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Release()
		var policy string
		if err := conn.QueryRow(ctx, "SHOW plan_cache_mode").Scan(&policy); err != nil {
			t.Fatal(err)
		}
		if policy != "force_custom_plan" {
			t.Fatalf("URI ignored: %s", policy)
		}
		tx, err := conn.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var transactionPolicy string
		err = tx.QueryRow(ctx, "SHOW plan_cache_mode").Scan(&transactionPolicy)
		rollbackErr := tx.Rollback(ctx)
		if err != nil || rollbackErr != nil || transactionPolicy != policy {
			t.Fatalf("transaction policy=%s: query=%v rollback=%v", transactionPolicy, err, rollbackErr)
		}
		report.URIConnections = append(report.URIConnections, policy)
	}
	var unchanged string
	if err := root.QueryRow(ctx, "SHOW plan_cache_mode").Scan(&unchanged); err != nil || unchanged != report.Settings["plan_cache_mode"] {
		t.Fatalf("policy leaked to fixture pool: %s / %v", unchanged, err)
	}
}

func policyResultHash(docs []rest.Doc, ordered bool) string {
	ids := make([]string, len(docs))
	for i, d := range docs {
		ids[i] = fmt.Sprint(d["_id"])
	}
	return policyIDsHash(ids, ordered)
}
func policyIDsHash(ids []string, ordered bool) string {
	if !ordered {
		sort.Strings(ids)
	}
	return fmt.Sprintf("%x", sha256.Sum256([]byte(strings.Join(ids, "\n"))))
}
