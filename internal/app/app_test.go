package app_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Gui-Rondon/hyrule/internal/app"
	"github.com/Gui-Rondon/hyrule/internal/platform/config"
	"github.com/Gui-Rondon/hyrule/internal/platform/mongodb"
	"github.com/Gui-Rondon/hyrule/internal/platform/server"
	"github.com/Gui-Rondon/hyrule/internal/rest"
	"github.com/Gui-Rondon/hyrule/internal/testkit"
)

func itemsSpec() app.Spec {
	return app.Spec{
		Name:      "items-api",
		DefaultDB: "items",
		Version:   "1.2.3",
		Setup: func(_ context.Context, env *app.Env) error {
			env.API.Register(&rest.Resource{
				Name:            "items",
				Store:           env.Store("items"),
				Schema:          rest.Schema{"name": {Type: rest.TypeString}},
				ResourceMethods: []string{http.MethodGet, http.MethodPost},
				PublicMethods:   rest.PublicGET,
			})
			return nil
		},
	}
}

func freePort(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return fmt.Sprint(l.Addr().(*net.TCPAddr).Port)
}

// Run is what main does: CONFIG_FILE, MONGO_CONNECTION_STRING and PORT, then
// serve until the context ends.
func TestRunServesUntilCanceled(t *testing.T) {
	db := testkit.Mongo(t)
	cfgFile := filepath.Join(t.TempDir(), "cfg.json")
	os.WriteFile(cfgFile, []byte(`{"auth": {"enable": "false"}}`), 0o600)
	port := freePort(t)
	t.Setenv("CONFIG_FILE", cfgFile)
	t.Setenv("PORT", port)
	// the database goes in the path: mongodb://host/<db>?options
	uri := os.Getenv("MONGO_TEST_URI")
	if i := strings.Index(uri, "/?"); i >= 0 {
		uri = uri[:i] + "/" + db.Name() + uri[i+1:]
	} else {
		uri = strings.TrimRight(uri, "/") + "/" + db.Name()
	}
	t.Setenv("MONGO_CONNECTION_STRING", uri)

	// a background task runs while serving and Run waits for it to stop
	spec := itemsSpec()
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
		env.Go("failing", func(context.Context) error { return errors.New("logged, not fatal") })
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

// Build alone does not start the tasks (tests drive them directly).
func TestBuildDoesNotStartTasks(t *testing.T) {
	db := testkit.Mongo(t)
	spec := itemsSpec()
	setup := spec.Setup
	spec.Setup = func(ctx context.Context, env *app.Env) error {
		env.Go("never", func(context.Context) error {
			t.Error("task started by Build")
			return nil
		})
		return setup(ctx, env)
	}
	_, err := app.Build(context.Background(), spec, app.Deps{
		Config: config.Config{}, Mongo: db.Client(), DB: db, Auth: testkit.DisabledAuth(t),
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestRunFailsWithoutConfiguration(t *testing.T) {
	t.Setenv("CONFIG_FILE", filepath.Join(t.TempDir(), "missing.json"))
	if err := app.Run(context.Background(), itemsSpec()); err == nil {
		t.Fatal("expected error")
	}
}

func TestSetupErrorStopsBuild(t *testing.T) {
	db := testkit.Mongo(t)
	spec := itemsSpec()
	spec.Setup = func(context.Context, *app.Env) error { return fmt.Errorf("boom") }
	_, err := app.Build(context.Background(), spec, app.Deps{Config: config.Config{}, Mongo: db.Client(), DB: db, Auth: testkit.DisabledAuth(t)})
	if err == nil || err.Error() != "boom" {
		t.Errorf("err = %v", err)
	}
}

func TestAuthenticatorFromConfig(t *testing.T) {
	a, err := app.AuthenticatorFromConfig(config.Config{
		"auth":  map[string]any{"enable": "t"},
		"azure": map[string]any{"issuer": "https://login/v2.0", "audiences": []any{"a"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !a.Enabled() || a.Azure() == nil {
		t.Errorf("enabled=%v azure=%v", a.Enabled(), a.Azure())
	}
	if _, err := app.AuthenticatorFromConfig(config.Config{"auth": map[string]any{"public_key_file": "/nao/existe"}}); err == nil {
		t.Error("missing public key must fail")
	}
}

type checker struct{ ok bool }

func (c checker) Check(context.Context) (bool, string) { return c.ok, "msg" }

func TestElasticsearchCheck(t *testing.T) {
	c := app.ElasticsearchCheck("elasticearch_healthcheck", checker{ok: true})
	if ok, _ := c.Check(context.Background()); c.Name != "elasticearch_healthcheck" || !ok {
		t.Errorf("checker = %+v", c)
	}
}

// D-27: the service file (Spec.Indexes, embedded from <service>.indices.conf)
// plus mongo.indexes from config-files are built at startup.
func TestBuildEnsuresDeclaredIndexes(t *testing.T) {
	db := testkit.Mongo(t)
	spec := itemsSpec()
	spec.Indexes = []byte("items name:1 unique\n")
	cfg := config.Config{"mongo": map[string]any{"indexes": []any{"items _updated:-1"}}}
	if _, err := app.Build(context.Background(), spec, app.Deps{Config: cfg, Mongo: db.Client(), DB: db, Auth: testkit.DisabledAuth(t)}); err != nil {
		t.Fatal(err)
	}
	if tdb := (&testkit.DB{Database: db}); !tdb.HasIndex(t, "items", "name") || !tdb.HasIndex(t, "items", "_updated") {
		t.Error("declared indexes not created")
	}

	// check mode creates nothing
	other := testkit.Mongo(t)
	cfg = config.Config{"mongo": map[string]any{"indexes_mode": "check"}}
	if _, err := app.Build(context.Background(), spec, app.Deps{Config: cfg, Mongo: other.Client(), DB: other, Auth: testkit.DisabledAuth(t)}); err != nil {
		t.Fatal(err)
	}
	if (&testkit.DB{Database: other}).HasIndex(t, "items", "name") {
		t.Error("check mode created an index")
	}
}

func TestBuildRejectsBadMongoConfig(t *testing.T) {
	db := testkit.Mongo(t)
	for name, cfg := range map[string]config.Config{
		"bad file line":    {},
		"bad timeout":      {"mongo": map[string]any{"query_timeout": "dez"}},
		"bad indexes_mode": {"mongo": map[string]any{"indexes_mode": "x"}},
		"bad config index": {"mongo": map[string]any{"indexes": []any{"items"}}},
	} {
		spec := itemsSpec()
		if name == "bad file line" {
			spec.Indexes = []byte("items name:9")
		}
		if _, err := app.Build(context.Background(), spec, app.Deps{Config: cfg, Mongo: db.Client(), DB: db, Auth: testkit.DisabledAuth(t)}); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

// /metrics is served at the root, with the mux routes of the API as labels.
func TestBuildServesMetrics(t *testing.T) {
	db := testkit.Mongo(t)
	handler, err := app.Build(context.Background(), itemsSpec(), app.Deps{
		Config: config.Config{}, Mongo: db.Client(), DB: db, Auth: testkit.DisabledAuth(t),
	})
	if err != nil {
		t.Fatal(err)
	}
	c := testkit.NewClient(t, handler)
	c.Do("GET", "/items", nil)
	res := c.Do("GET", "/metrics", nil)
	want := `http_server_request_duration_seconds_count{dry_run="false",method="GET",route="/items",service="items-api",status="200"} 1`
	if res.Status != http.StatusOK || !strings.Contains(res.Raw, want) {
		t.Errorf("/metrics = %d, want %s in\n%s", res.Status, want, res.Raw)
	}
}

// The route of a request served again internally through the API is the
// route of entry, with and without dry run, and a panic keeps its route.
func TestMetricsRouteOfEntry(t *testing.T) {
	db := testkit.Mongo(t)
	if _, rs, err := mongodb.TransactorFor(context.Background(), db.Client()); err != nil || !rs {
		t.Skip("dry run needs a replica set")
	}
	spec := itemsSpec()
	setup := spec.Setup
	spec.Setup = func(ctx context.Context, env *app.Env) error {
		if err := setup(ctx, env); err != nil {
			return err
		}
		env.API.Handle("POST /items/copy", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r2 := r.Clone(r.Context())
			r2.URL.Path = "/items"
			env.API.ServeHTTP(w, r2)
		}))
		env.API.Handle("GET /boom", http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("boom") }))
		return nil
	}
	for _, dryRun := range []bool{false, true} {
		guarded, guard := testkit.GuardedMongo(t, db)
		handler, err := app.Build(context.Background(), spec, app.Deps{
			Config: config.Config{}, Mongo: guarded.Client(), DB: guarded, Auth: testkit.DisabledAuth(t),
			DryRun: dryRun, WriteGuard: guard,
		})
		if err != nil {
			t.Fatal(err)
		}
		c := testkit.NewClient(t, handler)
		if res := c.Do("POST", "/items/copy", map[string]any{"name": "a"}); res.Status != http.StatusCreated {
			t.Fatalf("dry run %v: copy = %d %s", dryRun, res.Status, res.Raw)
		}
		func() {
			defer func() { recover() }()
			c.Do("GET", "/boom", nil)
		}()
		out := c.Do("GET", "/metrics", nil).Raw
		label := fmt.Sprintf(`dry_run="%v"`, dryRun)
		for _, want := range []string{
			`http_server_request_duration_seconds_count{` + label + `,method="POST",route="POST /items/copy",service="items-api",status="201"} 1`,
			`http_server_request_duration_seconds_count{` + label + `,method="GET",route="GET /boom",service="items-api",status="500"} 1`,
		} {
			if !strings.Contains(out, want) {
				t.Errorf("missing %s in\n%s", want, out)
			}
		}
		if strings.Contains(out, `method="POST",route="/items"`) {
			t.Errorf("dry run %v: the inner route was counted", dryRun)
		}
	}
}

// /status, /healthcheck (probes and monitoring) and /metrics (Prometheus),
// called every few seconds, stay out of the access log while they pass; the
// API routes are logged.
func TestHealthRoutesAreQuiet(t *testing.T) {
	db := testkit.Mongo(t)
	handler, err := app.Build(context.Background(), itemsSpec(), app.Deps{
		Config: config.Config{}, Mongo: db.Client(), DB: db, Auth: testkit.DisabledAuth(t),
	})
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	c := testkit.NewClient(t, server.AccessLog(handler))
	for _, path := range []string{"/status", "/healthcheck", "/items/status", "/items/healthcheck", "/metrics", "/items"} {
		c.Do("GET", path, nil)
	}
	var logged []string
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		var rec map[string]any
		if json.Unmarshal([]byte(line), &rec) == nil && rec["msg"] == "request" {
			logged = append(logged, fmt.Sprint(rec["path"]))
		}
	}
	if len(logged) != 1 || logged[0] != "/items" {
		t.Errorf("access log = %v, want only /items", logged)
	}
}

// /status/ and /healthcheck/ answer as /status and /healthcheck, also under
// a URL prefix (auth-api), as the Eve APIs (strict_slashes = False).
func TestHealthWithTrailingSlash(t *testing.T) {
	db := testkit.Mongo(t)
	for _, prefix := range []string{"", "/auth"} {
		spec := itemsSpec()
		spec.URLPrefix = prefix
		handler, err := app.Build(context.Background(), spec, app.Deps{
			Config: config.Config{}, Mongo: db.Client(), DB: db, Auth: testkit.DisabledAuth(t),
		})
		if err != nil {
			t.Fatal(err)
		}
		c := testkit.NewClient(t, handler)
		for _, path := range []string{"/status/", "/healthcheck/", "/items/status/"} {
			if res := c.Do("GET", prefix+path, nil); res.Status != http.StatusOK {
				t.Errorf("GET %s%s = %d", prefix, path, res.Status)
			}
		}
	}
}

// stripURLPrefix's own branches, under a non-empty URLPrefix: a request
// for exactly the prefix (no trailing path), one entirely outside the
// prefix, and one that merely starts with the same characters without
// being a path under it (/authzzz is not under /auth).
func TestStripURLPrefixEdgeCases(t *testing.T) {
	db := testkit.Mongo(t)
	spec := itemsSpec()
	spec.URLPrefix = "/auth"
	handler, err := app.Build(context.Background(), spec, app.Deps{
		Config: config.Config{}, Mongo: db.Client(), DB: db, Auth: testkit.DisabledAuth(t),
	})
	if err != nil {
		t.Fatal(err)
	}
	c := testkit.NewClient(t, handler)

	// Exactly the prefix, no trailing path: tail becomes "/", not "" —
	// the inner router's own root (the Eve-style resource index) answers,
	// instead of this 404ing as if the path were outside the prefix.
	if res := c.Do("GET", "/auth", nil); res.Status != http.StatusOK {
		t.Errorf("GET /auth = %d, want 200 from the inner router's root", res.Status)
	}

	// Outside the prefix entirely.
	if res := c.Do("GET", "/other", nil); res.Status != http.StatusNotFound {
		t.Errorf("GET /other = %d, want 404 (outside the URL prefix)", res.Status)
	}

	// Starts with the same characters as the prefix, but isn't a path
	// under it — must not be treated as a prefix match.
	if res := c.Do("GET", "/authzzz", nil); res.Status != http.StatusNotFound {
		t.Errorf("GET /authzzz = %d, want 404 (not actually under /auth)", res.Status)
	}
}
