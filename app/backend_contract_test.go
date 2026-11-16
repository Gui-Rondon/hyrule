package app_test

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/Gui-Rondon/hyrule/app"
	"github.com/Gui-Rondon/hyrule/platform/postgres"
	"github.com/Gui-Rondon/hyrule/rest"
	"github.com/Gui-Rondon/hyrule/restpostgres"
	"github.com/Gui-Rondon/hyrule/testkit"
)

func TestUnknownBackendFailsBeforeDependencies(t *testing.T) {
	for _, backend := range []app.Backend{"postgre", "mongo", "POSTGRES"} {
		spec := app.Spec{Name: "invalid-backend", Backend: backend, Setup: func(context.Context, *app.Env) error { t.Fatal("unknown backend reached Setup"); return nil }}
		if _, err := app.Build(context.Background(), spec, app.Deps{}); err == nil || !strings.Contains(err.Error(), "unknown backend") {
			t.Fatalf("Build(%q)=%v", backend, err)
		}
		if err := app.Run(context.Background(), spec); err == nil || !strings.Contains(err.Error(), "unknown backend") {
			t.Fatalf("Run(%q)=%v", backend, err)
		}
	}
}

// Exercise the documented Go registration path against both actual databases.
// IDs and ETags remain backend-specific; the shared HTTP contract does not.
func TestBackendHTTPContract(t *testing.T) {
	for _, backend := range []app.Backend{app.BackendMongo, app.BackendPostgres} {
		name := "mongo-default"
		if backend == app.BackendPostgres {
			name = "postgres"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			deps := app.Deps{Auth: testkit.DisabledAuth(t)}
			table := "items"
			if backend == app.BackendMongo {
				db := testkit.Mongo(t)
				deps.DB = db
				deps.Mongo = db.Client()
			} else {
				deps.Postgres = postgresTestPool(t)
				table = postgresTestTable(t, deps.Postgres)
			}
			schema := rest.Schema{"name": {Type: rest.TypeString, Required: true}}
			spec := app.Spec{Name: "contract-api", Backend: backend, URLPrefix: "api", Setup: func(_ context.Context, env *app.Env) error {
				env.Wrap(func(next http.Handler) http.Handler {
					return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						w.Header().Set("X-Backend-Contract", "active")
						next.ServeHTTP(w, r)
					})
				})
				res := &rest.Resource{Name: "items", Schema: schema, ResourceMethods: []string{http.MethodGet, http.MethodPost}, ItemMethods: []string{http.MethodGet, http.MethodPatch, http.MethodPut, http.MethodDelete}}
				if backend == app.BackendMongo {
					if env.DB == nil || env.Postgres != nil {
						t.Fatal("Mongo env mixed dependencies")
					}
					res.Store = env.Store(table)
				} else {
					if env.DB != nil || env.Postgres != deps.Postgres || env.PostgresTx.Pool != deps.Postgres {
						t.Fatal("Postgres env mixed dependencies")
					}
					adapter, err := restpostgres.NewAdapter(postgres.NewStore(env.Postgres, table), env.PostgresTx, restpostgres.Options{Schema: schema, Retry: rest.DefaultRetryOptions()})
					if err != nil {
						return err
					}
					res.Adapter = adapter
				}
				env.API.Register(res)
				return nil
			}}
			h, err := app.Build(ctx, spec, deps)
			if err != nil {
				t.Fatal(err)
			}
			c := testkit.NewClient(t, h)
			request := func(method, path string, body any, status int, headers ...string) *testkit.Response {
				t.Helper()
				r := c.Do(method, path, body, headers...)
				if r.Status != status {
					t.Fatalf("%s %s=%d %s", method, path, r.Status, r.Raw)
				}
				return r
			}
			request("GET", "/items", nil, 404)
			r := request("POST", "/api/items", map[string]any{}, 422)
			r = request("POST", "/api/items", map[string]any{"name": "first"}, 201)
			id, etag := r.String("_id"), r.String("_etag")
			if id == "" || etag == "" {
				t.Fatal("missing ID/ETag")
			}
			if backend == app.BackendMongo {
				if _, err := bson.ObjectIDFromHex(id); err != nil {
					t.Fatal(err)
				}
			} else {
				if _, err := uuid.Parse(id); err != nil {
					t.Fatal(err)
				}
			}
			path := "/api/items/" + id
			got := request("GET", path, nil, 200)
			if got.String("name") != "first" || got.Header.Get("X-Backend-Contract") != "active" {
				t.Fatal("registration/middleware contract changed")
			}
			request("PATCH", path, map[string]any{"name": "second"}, 412, "If-Match", `"stale"`)
			r = request("PATCH", path, map[string]any{"name": "second"}, 200, "If-Match", `"`+etag+`"`)
			etag = r.String("_etag")
			r = request("PUT", path, map[string]any{"name": "third"}, 200, "If-Match", `"`+etag+`"`)
			etag = r.String("_etag")
			if got := request("GET", path, nil, 200); got.String("name") != "third" {
				t.Fatal("replace not persisted")
			}
			request("GET", `/api/items?where={"name":"third"}&sort=name&max_results=1`, nil, 200)
			request("DELETE", path, nil, 204, "If-Match", `"`+etag+`"`)
			request("GET", path, nil, 404)
			request("GET", "/api/status", nil, 200)
			request("GET", "/api/healthcheck", nil, 200)
			request("GET", "/metrics", nil, 200)
		})
	}
}

// Even an early Setup failure must cancel/join workers before closing their DB.
func TestRunStopsWorkersBeforeDatabaseClose(t *testing.T) {
	for _, backend := range []app.Backend{app.BackendMongo, app.BackendPostgres} {
		name := "mongo"
		if backend == app.BackendPostgres {
			name = "postgres"
		}
		t.Run(name, func(t *testing.T) {
			if backend == app.BackendMongo {
				db := testkit.Mongo(t)
				uri, err := url.Parse(os.Getenv("MONGO_TEST_URI"))
				if err != nil {
					t.Fatal(err)
				}
				uri.Path = "/" + db.Name()
				t.Setenv("MONGO_CONNECTION_STRING", uri.String())
			} else {
				uri := os.Getenv("POSTGRES_TEST_URI")
				if uri == "" {
					t.Skip("POSTGRES_TEST_URI not set")
				}
				t.Setenv("POSTGRES_CONNECTION_STRING", uri)
			}
			cfg := filepath.Join(t.TempDir(), "config.json")
			if err := os.WriteFile(cfg, []byte(`{"auth":{"enable":false}}`), 0600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("CONFIG_FILE", cfg)
			t.Setenv("DRY_RUN", "")
			started := make(chan struct{})
			finished := make(chan error, 1)
			setupErr := errors.New("setup intentionally failed")
			spec := app.Spec{Name: "worker-cleanup", Backend: backend, Setup: func(ctx context.Context, env *app.Env) error {
				env.Go("cleanup", func(ctx context.Context) error {
					close(started)
					<-ctx.Done()
					cleanup, cancel := context.WithTimeout(context.Background(), 3*time.Second)
					defer cancel()
					var err error
					if backend == app.BackendMongo {
						err = env.DB.Client().Ping(cleanup, nil)
					} else {
						err = env.Postgres.Ping(cleanup)
					}
					finished <- err
					return nil
				})
				select {
				case <-started:
					return setupErr
				case <-ctx.Done():
					return ctx.Err()
				}
			}}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if err := app.Run(ctx, spec); !errors.Is(err, setupErr) {
				t.Fatalf("Run error=%v", err)
			}
			select {
			case err := <-finished:
				if err != nil {
					t.Fatalf("database closed before worker cleanup: %v", err)
				}
			default:
				t.Fatal("Run returned before worker cleanup")
			}
		})
	}
}
