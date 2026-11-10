package app_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/Gui-Rondon/hyrule/internal/app"
	"github.com/Gui-Rondon/hyrule/internal/evecompat"
	"github.com/Gui-Rondon/hyrule/internal/platform/config"
	"github.com/Gui-Rondon/hyrule/internal/platform/mongodb"
	"github.com/Gui-Rondon/hyrule/internal/testkit"
)

func TestDryRunSetting(t *testing.T) {
	cases := []struct {
		env  string
		want bool
		err  bool
	}{
		{"", false, false},
		{"true", true, false},
		{"1", true, false},
		{"yes", true, false},
		{"false", false, false},
		{"sim", false, true},
		{"talvez", false, true},
	}
	for _, tc := range cases {
		t.Setenv("DRY_RUN", tc.env)
		got, err := app.DryRun()
		if got != tc.want || (err != nil) != tc.err {
			t.Errorf("DRY_RUN=%q: %v, %v", tc.env, got, err)
		}
	}
}

func TestDryRunTimeoutSetting(t *testing.T) {
	cases := []struct {
		env  string
		want time.Duration
		err  bool
	}{
		{"", 0, false},
		{"20s", 20 * time.Second, false},
		{"500ms", 500 * time.Millisecond, false},
		{"0s", 0, true},
		{"-1s", 0, true},
		{"20", 0, true},
	}
	for _, tc := range cases {
		t.Setenv("DRY_RUN_TIMEOUT", tc.env)
		got, err := app.DryRunTimeout()
		if got != tc.want || (err != nil) != tc.err {
			t.Errorf("DRY_RUN_TIMEOUT=%q: %v, %v", tc.env, got, err)
		}
	}
}

// The config-files are shared with the Python API and stay at the cutover:
// a dry_run key there is refused, whatever its value, so the dry run is
// never left on when the facade points to Go.
func TestDryRunKeyRefused(t *testing.T) {
	db := testkit.Mongo(t)
	for _, v := range []any{true, false, "yes"} {
		_, err := app.Build(context.Background(), itemsSpec(), app.Deps{
			Config: config.Config{"dry_run": v}, Mongo: db.Client(), DB: db, Auth: testkit.DisabledAuth(t),
		})
		if err == nil || !strings.Contains(err.Error(), "DRY_RUN") {
			t.Errorf("dry_run: %v accepted: %v", v, err)
		}
	}
}

// In dry run the service answers as usual and stores nothing: the writes
// roll back, the post-write hooks and the background tasks do not run, the
// missing indexes are only reported, and a route with effects outside Mongo
// answers 503.
func TestBuildInDryRun(t *testing.T) {
	db := testkit.Mongo(t)
	if _, rs, err := mongodb.TransactorFor(context.Background(), db.Client()); err != nil || !rs {
		t.Skip("dry run needs a replica set")
	}
	var post int
	var sawDryRun bool
	spec := app.Spec{
		Name: "items-api", DefaultDB: "items", Version: "1",
		Indexes: []byte("items name:1 unique\n"),
		Setup: func(_ context.Context, env *app.Env) error {
			sawDryRun = env.DryRun
			env.API.Register(&evecompat.Resource{
				Name:            "items",
				Store:           env.Store("items"),
				Schema:          evecompat.Schema{"name": {Type: evecompat.TypeString}},
				ResourceMethods: []string{http.MethodGet, http.MethodPost},
				Hooks: evecompat.Hooks{OnInserted: []func(context.Context, []evecompat.Doc) error{
					func(context.Context, []evecompat.Doc) error { post++; return nil },
				}},
			})
			env.API.Handle("POST /login", env.RefuseInDryRun("LDAP bind", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				t.Error("refused route served")
			})))
			env.Go("relay", func(context.Context) error { return nil })
			return nil
		},
	}
	var started []string
	guarded, guard := testkit.GuardedMongo(t, db)
	handler, err := app.Build(context.Background(), spec, app.Deps{
		Config: config.Config{}, Mongo: guarded.Client(), DB: guarded, Auth: testkit.DisabledAuth(t),
		Start:  func(name string, _ func(context.Context) error) { started = append(started, name) },
		DryRun: true, WriteGuard: guard,
	})
	if err != nil {
		t.Fatal(err)
	}
	c := testkit.NewClient(t, handler)
	res := c.Do("POST", "/items", map[string]any{"name": "a"})
	if res.Status != http.StatusCreated || res.Header.Get("X-Dry-Run") != "true" {
		t.Errorf("POST = %d %s, X-Dry-Run %q", res.Status, res.Raw, res.Header.Get("X-Dry-Run"))
	}
	if n, _ := db.Collection("items").CountDocuments(context.Background(), bson.M{}); n != 0 {
		t.Errorf("%d documents stored", n)
	}
	if post != 0 || len(started) != 0 || !sawDryRun {
		t.Errorf("post hooks %d, tasks started %v, Env.DryRun %v", post, started, sawDryRun)
	}
	if (&testkit.DB{Database: db}).HasIndex(t, "items", "name") {
		t.Error("index built in dry run")
	}
	res = c.Do("POST", "/login", nil)
	if res.Status != http.StatusServiceUnavailable || !strings.Contains(res.Raw, "dry run: LDAP bind") || res.Header.Get("X-Dry-Run") != "true" {
		t.Errorf("refused route = %d %s", res.Status, res.Raw)
	}
	// the mode is checked on /status and /healthcheck before the facade changes
	for _, path := range []string{"/status", "/healthcheck"} {
		if res := c.Do("GET", path, nil); res.Status != http.StatusOK || res.Map()["dry_run"] != true {
			t.Errorf("%s = %d %s", path, res.Status, res.Raw)
		}
	}

	if guard.Violations() != 0 {
		t.Errorf("%d writes outside a transaction", guard.Violations())
	}

	// a write outside a transaction fails /healthcheck (a new build: the
	// passing result above is cached)
	guarded, guard = testkit.GuardedMongo(t, db)
	handler, err = app.Build(context.Background(), spec, app.Deps{
		Config: config.Config{}, Mongo: guarded.Client(), DB: guarded, Auth: testkit.DisabledAuth(t),
		DryRun: true, WriteGuard: guard,
	})
	if err != nil {
		t.Fatal(err)
	}
	guarded.Collection("items").InsertOne(context.Background(), bson.M{"name": "outside"})
	res = testkit.NewClient(t, handler).Do("GET", "/healthcheck", nil)
	if res.Status != http.StatusInternalServerError || !strings.Contains(res.Raw, "Dry run NOK: 1 writes outside a transaction") {
		t.Errorf("healthcheck after a write outside a transaction = %d %s", res.Status, res.Raw)
	}
	_, err = app.Build(context.Background(), spec, app.Deps{Config: config.Config{}, Mongo: db.Client(), DB: db, Auth: testkit.DisabledAuth(t), DryRun: true})
	if err == nil || !strings.Contains(err.Error(), "WriteGuard") {
		t.Errorf("dry run without WriteGuard: %v", err)
	}

	// a service with effects that cannot be rolled back does not start
	spec.NoDryRun = "it only runs background jobs"
	_, err = app.Build(context.Background(), spec, app.Deps{Config: config.Config{}, Mongo: db.Client(), DB: db, Auth: testkit.DisabledAuth(t), DryRun: true})
	if err == nil || !strings.Contains(err.Error(), "it only runs background jobs") {
		t.Errorf("NoDryRun: %v", err)
	}
}

// Without dry run the same routes write and the tasks start.
func TestBuildWithoutDryRun(t *testing.T) {
	db := testkit.Mongo(t)
	var started []string
	handler, err := app.Build(context.Background(), itemsSpec(), app.Deps{
		Config: config.Config{}, Mongo: db.Client(), DB: db, Auth: testkit.DisabledAuth(t),
		Start: func(name string, _ func(context.Context) error) { started = append(started, name) },
	})
	if err != nil {
		t.Fatal(err)
	}
	res := testkit.NewClient(t, handler).Do("POST", "/items", map[string]any{"name": "a"})
	if res.Status != http.StatusCreated || res.Header.Get("X-Dry-Run") != "" {
		t.Errorf("POST = %d, X-Dry-Run %q", res.Status, res.Header.Get("X-Dry-Run"))
	}
	if n, _ := db.Collection("items").CountDocuments(context.Background(), bson.M{}); n != 1 {
		t.Errorf("%d documents stored", n)
	}
	if len(started) != 0 {
		t.Errorf("itemsSpec has no tasks: %v", started)
	}
	// the Eve body, without dry_run
	if res := testkit.NewClient(t, handler).Do("GET", "/status", nil); res.Map()["dry_run"] != nil {
		t.Errorf("status = %s", res.Raw)
	}
}
