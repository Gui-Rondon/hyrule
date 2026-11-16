package restpostgres

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Gui-Rondon/hyrule/platform/postgres"
	"github.com/Gui-Rondon/hyrule/rest"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestSwaggerPostgresWithoutDatabaseConnection(t *testing.T) {
	// Construct a lazy pool pointed at an unavailable DB. Serving documentation
	// must not connect to it or require PostgreSQL to be running.
	pool, err := pgxpool.New(context.Background(), "postgres://localhost:1/unavailable?connect_timeout=1")
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	adapter, err := NewAdapter(postgres.NewStore(pool, "items"), postgres.Transactor{Pool: pool}, Options{Schema: defaultHTTPTestSchema, Retry: rest.DefaultRetryOptions()})
	if err != nil {
		t.Fatal(err)
	}
	a := rest.New(rest.Options{})
	a.Register(&rest.Resource{Name: "items", Adapter: adapter, Schema: defaultHTTPTestSchema})
	w := httptest.NewRecorder()
	a.ServeHTTP(w, httptest.NewRequest("GET", "/api/openapi.json", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"/items/{lookup}"`) {
		t.Fatalf("Postgres default docs: %d %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "[0-9a-fA-F]{24}") {
		t.Fatal("ID incorrectly restricted to Mongo ObjectID")
	}
}
