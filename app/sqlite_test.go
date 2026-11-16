package app_test

import (
	"context"
	"errors"
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/Gui-Rondon/hyrule/app"
	"github.com/Gui-Rondon/hyrule/platform/config"
	"github.com/Gui-Rondon/hyrule/platform/postgres"
	"github.com/Gui-Rondon/hyrule/platform/sqlite"
)

func TestSQLiteBuild(t *testing.T) {
	ctx := context.Background()
	db, err := sqlite.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	cfg := config.Config{"auth": map[string]any{"enable": false}}
	spec := app.Spec{Name: "local", Backend: app.BackendSQLite, Setup: func(_ context.Context, env *app.Env) error {
		if env.SQLite != db || env.SQLiteTx.DB != db || env.DB != nil || env.Postgres != nil {
			t.Fatal("mixed dependencies")
		}
		return nil
	}}
	deps := app.Deps{SQLite: db, Config: cfg}
	h, err := app.Build(ctx, spec, deps)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/status", "/healthcheck"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 200 {
			t.Fatalf("%s %d", path, w.Code)
		}
	}
	db.Close()
	h, err = app.Build(ctx, spec, deps)
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/healthcheck", nil))
	if w.Code == 200 {
		t.Fatal("closed db healthy")
	}
	cases := []struct {
		name string
		edit func(*app.Spec, *app.Deps)
	}{
		{"missing db", func(_ *app.Spec, d *app.Deps) { d.SQLite = nil }},
		{"dry run", func(_ *app.Spec, d *app.Deps) { d.DryRun = true }},
		{"mongo indexes", func(s *app.Spec, _ *app.Deps) { s.Indexes = []byte("[]") }},
		{"postgres indexes", func(s *app.Spec, _ *app.Deps) { s.PostgresIndexes = []postgres.IndexSpec{{}} }},
		{"incompatible config", func(_ *app.Spec, d *app.Deps) {
			d.Config = config.Config{"auth": map[string]any{"enable": false}, "outbox": map[string]any{}}
		}},
		{"timeout", func(_ *app.Spec, d *app.Deps) {
			d.Config = config.Config{"auth": map[string]any{"enable": false}, "sqlite": map[string]any{"query_timeout": "bad"}}
		}},
		{"setup", func(s *app.Spec, _ *app.Deps) {
			s.Setup = func(context.Context, *app.Env) error { return errors.New("setup failed") }
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, d := spec, deps
			tc.edit(&s, &d)
			if _, err := app.Build(ctx, s, d); err == nil {
				t.Fatal("configuration accepted")
			}
		})
	}
}
func TestSQLiteRun(t *testing.T) {
	t.Setenv("CONFIG_FILE", "")
	t.Setenv("CONFIGSERVER_ADDRESS", "")
	t.Setenv("SQLITE_PATH", filepath.Join(t.TempDir(), "local.db"))
	t.Setenv("DRY_RUN", "false")
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	t.Setenv("PORT", strconv.Itoa(port))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ready := make(chan struct{})
	done := make(chan error, 1)
	spec := app.Spec{Name: "sqlite-run", Backend: app.BackendSQLite, Setup: func(_ context.Context, env *app.Env) error { close(ready); return nil }}
	go func() { done <- app.Run(ctx, spec) }()
	select {
	case <-ready:
	case err := <-done:
		t.Fatalf("startup %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("startup timed out")
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	t.Setenv("DRY_RUN", "true")
	if err := app.Run(context.Background(), spec); err == nil {
		t.Fatal("dry run accepted")
	}
	t.Setenv("DRY_RUN", "false")
	t.Setenv("SQLITE_PATH", filepath.Join(t.TempDir(), "missing", "db"))
	if err := app.Run(context.Background(), spec); err == nil {
		t.Fatal("bad path accepted")
	}
	t.Setenv("SQLITE_PATH", filepath.Join(t.TempDir(), "local.db"))
	spec.Setup = func(context.Context, *app.Env) error { return errors.New("setup") }
	if err := app.Run(context.Background(), spec); err == nil {
		t.Fatal("setup failure swallowed")
	}
	file := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(file, []byte(`{"auth":{"enable":false},"sqlite":{"path":":memory:"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CONFIG_FILE", file)
	if err := app.Run(context.Background(), spec); err == nil {
		t.Fatal("setup failure swallowed with file config")
	}
}
