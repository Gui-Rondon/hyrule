package app_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Gui-Rondon/hyrule/app"
	"github.com/Gui-Rondon/hyrule/platform/config"
	"github.com/Gui-Rondon/hyrule/platform/postgres"
	"github.com/Gui-Rondon/hyrule/rest"
	"github.com/Gui-Rondon/hyrule/restpostgres"
	"github.com/Gui-Rondon/hyrule/testkit"
)

func postgresTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	uri := os.Getenv("POSTGRES_TEST_URI")
	if uri == "" {
		t.Skip("POSTGRES_TEST_URI not set")
	}
	pool, err := pgxpool.New(context.Background(), uri)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// postgresTestTable creates the jsonb-document table shape restpostgres
// expects (same DDL as restpostgres/adapter_test.go's setupResourceTable
// and examples/blog-apis/services/comments-api's ensureSchema), dropped
// at cleanup.
func postgresTestTable(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	table := "app_test_" + strings.ToLower(strconv.FormatInt(time.Now().UnixNano(), 36))
	ctx := context.Background()
	ddl := `CREATE TABLE ` + table + ` (
		id         uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
		doc        jsonb       NOT NULL DEFAULT '{}',
		created_at timestamptz NOT NULL DEFAULT now(),
		updated_at timestamptz NOT NULL DEFAULT now(),
		version    bigint      NOT NULL DEFAULT 1,
		deleted    boolean     NOT NULL DEFAULT false
	)`
	if _, err := pool.Exec(ctx, ddl); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pool.Exec(context.Background(), "DROP TABLE IF EXISTS "+table) })
	return table
}

var itemsPostgresSchema = rest.Schema{"name": {Type: rest.TypeString}}

// itemsPostgresSpec mirrors itemsSpec (the Mongo one) over a Postgres-
// backed resource: same registration shape a real BackendPostgres
// service's Setup uses (restpostgres.NewAdapter over env.Postgres/
// env.PostgresTx, assigned to Resource.Adapter -- postgres.Store never
// satisfies rest.Store, so there is no env.Store(table) equivalent).
func itemsPostgresSpec(table string) app.Spec {
	return app.Spec{
		Name:      "items-api",
		Backend:   app.BackendPostgres,
		DefaultDB: "items",
		Version:   "1.2.3",
		PostgresIndexes: []postgres.IndexSpec{
			{Table: table, Fields: []postgres.IndexField{{Field: "name"}}},
		},
		Setup: func(_ context.Context, env *app.Env) error {
			adapter, err := restpostgres.NewAdapter(postgres.NewStore(env.Postgres, table), env.PostgresTx, restpostgres.Options{
				Schema: itemsPostgresSchema,
				Retry:  rest.DefaultRetryOptions(),
			})
			if err != nil {
				return err
			}
			env.API.Register(&rest.Resource{
				Name:            "items",
				Schema:          itemsPostgresSchema,
				Adapter:         adapter,
				ResourceMethods: []string{http.MethodGet, http.MethodPost},
				PublicMethods:   rest.PublicGET,
			})
			return nil
		},
	}
}

// TestBuildPostgresServesResource proves A4+B1 end to end at the Build
// level: a real pool (platform/postgres.Connect's own counterpart,
// Deps.Postgres), a real table, a resource registered the way a
// BackendPostgres Setup actually would, served over real HTTP.
func TestBuildPostgresServesResource(t *testing.T) {
	pool := postgresTestPool(t)
	table := postgresTestTable(t, pool)
	handler, err := app.Build(context.Background(), itemsPostgresSpec(table), app.Deps{
		Config: config.Config{}, Postgres: pool, Auth: testkit.DisabledAuth(t),
	})
	if err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/items", strings.NewReader(`{"name":"a"}`))
	req.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /items = %d: %s", rec.Code, rec.Body)
	}

	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/items", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /items = %d: %s", rec.Code, rec.Body)
	}
	var list struct {
		Items []map[string]any `json:"_items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Items) != 1 || list.Items[0]["name"] != "a" {
		t.Fatalf("items = %v", list.Items)
	}

	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/items/healthcheck", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("healthcheck = %d: %s", rec.Code, rec.Body)
	}
}

// TestBuildPostgresEnsuresDeclaredIndexes mirrors
// TestBuildEnsuresDeclaredIndexes (the Mongo one): Build's
// BackendPostgres path must call postgres.EnsureIndexes for
// Spec.PostgresIndexes, against the real catalog.
func TestBuildPostgresEnsuresDeclaredIndexes(t *testing.T) {
	pool := postgresTestPool(t)
	table := postgresTestTable(t, pool)
	spec := itemsPostgresSpec(table)
	if _, err := app.Build(context.Background(), spec, app.Deps{
		Config: config.Config{}, Postgres: pool, Auth: testkit.DisabledAuth(t),
	}); err != nil {
		t.Fatal(err)
	}
	var exists bool
	err := pool.QueryRow(context.Background(),
		`SELECT EXISTS (SELECT 1 FROM pg_indexes WHERE schemaname = current_schema() AND tablename = $1 AND indexname = $2)`,
		table, table+"_name_idx").Scan(&exists)
	if err != nil {
		t.Fatal(err)
	}
	if !exists {
		t.Error("declared index was not created")
	}
}

// TestBuildPostgresNoDryRun mirrors the Mongo-side NoDryRun check
// (app/dryrun_test.go's TestBuildDryRun) for BackendPostgres: a service
// with effects that cannot be rolled back refuses to start in dry run.
func TestBuildPostgresNoDryRun(t *testing.T) {
	pool := postgresTestPool(t)
	table := postgresTestTable(t, pool)
	spec := itemsPostgresSpec(table)
	spec.NoDryRun = "it only runs background jobs"
	_, err := app.Build(context.Background(), spec, app.Deps{
		Config: config.Config{}, Postgres: pool, Auth: testkit.DisabledAuth(t), DryRun: true,
	})
	if err == nil || !strings.Contains(err.Error(), "it only runs background jobs") {
		t.Errorf("NoDryRun: %v", err)
	}
}

// TestBuildPostgresRequiresPool: Deps.Postgres is not optional for
// BackendPostgres (unlike BackendMongo's Deps.DB, there is no "connect
// one from Deps.Config" fallback inside Build).
func TestBuildPostgresRequiresPool(t *testing.T) {
	_, err := app.Build(context.Background(), app.Spec{
		Name: "items-api", Backend: app.BackendPostgres, Version: "1.2.3",
		Setup: func(context.Context, *app.Env) error { return nil },
	}, app.Deps{Config: config.Config{}, Auth: testkit.DisabledAuth(t)})
	if err == nil {
		t.Error("Build must fail without Deps.Postgres")
	}
}

// TestEnvStorePanicsWithClearMessageForBackendPostgres: Env.Store is
// BackendMongo only (its own doc comment says so) -- calling it from a
// BackendPostgres Setup (e.g. a Mongo service's Setup adapted in a
// hurry) must fail loud with a message naming the mistake, not a bare
// nil-pointer dereference on e.DB.
func TestEnvStorePanicsWithClearMessageForBackendPostgres(t *testing.T) {
	pool := postgresTestPool(t)
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("env.Store must panic for BackendPostgres")
		}
		if msg, ok := r.(string); !ok || !strings.Contains(msg, "BackendMongo only") {
			t.Errorf("panic = %v, want a message naming BackendMongo only", r)
		}
	}()
	app.Build(context.Background(), app.Spec{
		Name: "items-api", Backend: app.BackendPostgres, Version: "1.2.3",
		Setup: func(_ context.Context, env *app.Env) error {
			env.Store("whatever")
			return nil
		},
	}, app.Deps{Config: config.Config{}, Postgres: pool, Auth: testkit.DisabledAuth(t)})
}

// TestBuildPostgresDryRunDoesNotPersist is B2's real proof: a write in
// dry run still answers as if it succeeded (X-Dry-Run header, 201), but
// the row is never actually committed -- postgres.Transactor{Rollback:
// true}, not a WriteGuard-equivalent, since every production write
// already goes through Store.Querier(ctx) (the ambient transaction
// Rollback discards).
func TestBuildPostgresDryRunDoesNotPersist(t *testing.T) {
	pool := postgresTestPool(t)
	table := postgresTestTable(t, pool)
	handler, err := app.Build(context.Background(), itemsPostgresSpec(table), app.Deps{
		Config: config.Config{}, Postgres: pool, Auth: testkit.DisabledAuth(t), DryRun: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/items", strings.NewReader(`{"name":"a"}`))
	req.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /items = %d: %s", rec.Code, rec.Body)
	}
	if rec.Header().Get("X-Dry-Run") != "true" {
		t.Errorf("X-Dry-Run header = %q, want true", rec.Header().Get("X-Dry-Run"))
	}

	var n int
	if err := pool.QueryRow(context.Background(), "SELECT count(*) FROM "+table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("rows in %s after a dry run write = %d, want 0 (nothing must persist)", table, n)
	}
}

// TestBuildPostgresDryRunDoesNotCreateDeclaredIndexes mirrors
// TestBuildPostgresEnsuresDeclaredIndexes but in dry run: creating an
// index is a write too, so indexesMode is downgraded from
// IndexesCreate to IndexesCheck the same way the Mongo side already
// does.
func TestBuildPostgresDryRunDoesNotCreateDeclaredIndexes(t *testing.T) {
	pool := postgresTestPool(t)
	table := postgresTestTable(t, pool)
	spec := itemsPostgresSpec(table)
	if _, err := app.Build(context.Background(), spec, app.Deps{
		Config: config.Config{}, Postgres: pool, Auth: testkit.DisabledAuth(t), DryRun: true,
	}); err != nil {
		t.Fatal(err)
	}
	var exists bool
	err := pool.QueryRow(context.Background(),
		`SELECT EXISTS (SELECT 1 FROM pg_indexes WHERE schemaname = current_schema() AND tablename = $1 AND indexname = $2)`,
		table, table+"_name_idx").Scan(&exists)
	if err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Error("dry run must not create the declared index")
	}
}

// TestRunServesUntilCanceledPostgres is TestRunServesUntilCanceled's
// Postgres counterpart: CONFIG_FILE, POSTGRES_CONNECTION_STRING and
// PORT, through Run's real platform/postgres.Connect, serving until the
// context ends -- the full A4+B1 path, not just Build.
func TestRunServesUntilCanceledPostgres(t *testing.T) {
	pool := postgresTestPool(t)
	table := postgresTestTable(t, pool)
	cfgFile := filepath.Join(t.TempDir(), "cfg.json")
	os.WriteFile(cfgFile, []byte(`{"auth": {"enable": "false"}}`), 0o600)
	port := freePort(t)
	t.Setenv("CONFIG_FILE", cfgFile)
	t.Setenv("PORT", port)
	t.Setenv("POSTGRES_CONNECTION_STRING", os.Getenv("POSTGRES_TEST_URI"))

	spec := itemsPostgresSpec(table)
	setup := spec.Setup
	started := make(chan struct{})
	var stopped atomic.Bool
	spec.Setup = func(ctx context.Context, env *app.Env) error {
		env.Go("probe", func(ctx context.Context) error {
			close(started)
			<-ctx.Done()
			time.Sleep(50 * time.Millisecond)
			stopped.Store(true)
			return nil
		})
		return setup(ctx, env)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- app.Run(ctx, spec) }()

	base := "http://127.0.0.1:" + port
	var status map[string]any
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if resp, err := http.Get(base + "/items/status"); err == nil {
			json.NewDecoder(resp.Body).Decode(&status)
			resp.Body.Close()
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if status["version"] != "1.2.3" || status["message"] != "Running..." {
		t.Fatalf("status = %v", status)
	}
	resp, err := http.Post(base+"/items", "application/json", strings.NewReader(`{"name":"a"}`))
	if err != nil || resp.StatusCode != http.StatusCreated {
		t.Fatalf("post: %v %v", err, resp)
	}
	resp, _ = http.Get(base + "/items/healthcheck")
	if resp.StatusCode != http.StatusOK {
		t.Errorf("healthcheck = %d", resp.StatusCode)
	}

	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("background task not started")
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("run: %v", err)
		}
	case <-time.After(25 * time.Second):
		t.Fatal("server did not stop")
	}
	if !stopped.Load() {
		t.Error("Run returned before the background task stopped")
	}
}
