package main

import (
	"context"
	"fmt"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Gui-Rondon/hyrule/platform/postgres"
	"github.com/Gui-Rondon/hyrule/rest"
	"github.com/Gui-Rondon/hyrule/restpostgres"
)

const commentsTable = "comments"

// commentsSchema: blog comments on a post -- the simplest resource
// that still exercises a real where= filter (post_id) and real
// optimistic concurrency (status transitions) end-to-end over HTTP,
// against a real Postgres-backed table instead of the framework's
// own tests' throwaway ones.
var commentsSchema = rest.Schema{
	"post_id":     {Type: rest.TypeString, Required: true},
	"author_name": {Type: rest.TypeString, Required: true},
	"body":        {Type: rest.TypeString, Required: true},
	"status":      {Type: rest.TypeString, Default: "pending"},
}

// commentsIndexes declares the lookup every comments page relies on
// (list a post's comments) -- Fase 6's declarative contract
// (platform/postgres/indexes.go), run here against a real resource a
// service actually serves, not just restpostgres's own throwaway test
// tables.
func commentsIndexes(table string) []postgres.IndexSpec {
	return []postgres.IndexSpec{
		{Table: table, Fields: []postgres.IndexField{{Field: "post_id"}}},
	}
}

// ensureSchema creates table if it doesn't exist yet, with the shape
// every restpostgres resource expects (restpostgres/adapter_test.go's
// setupResourceTable: id/doc/created_at/updated_at/version/deleted --
// no migration tool exists yet for a Postgres resource, so a real
// service creates its own table at startup, the same DDL a hand-written
// migration would run once), then ensures its declared indexes.
func ensureSchema(ctx context.Context, pool *pgxpool.Pool, table string) error {
	if err := postgres.EnsureFunctions(ctx, pool); err != nil {
		return err
	}
	ddl := fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
		id         uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
		doc        jsonb       NOT NULL DEFAULT '{}',
		created_at timestamptz NOT NULL DEFAULT now(),
		updated_at timestamptz NOT NULL DEFAULT now(),
		version    bigint      NOT NULL DEFAULT 1,
		deleted    boolean     NOT NULL DEFAULT false
	)`, table)
	if _, err := pool.Exec(ctx, ddl); err != nil {
		return err
	}
	_, err := postgres.EnsureIndexes(ctx, pool, commentsIndexes(table), postgres.IndexesCreate)
	return err
}

// setup wires a real postgresAdapter into a real rest.API -- the
// production equivalent of restpostgres/http_test.go's
// newHTTPTestAPI, minus the test-only table teardown.
func setup(ctx context.Context, pool *pgxpool.Pool, table string) (*rest.API, error) {
	if err := ensureSchema(ctx, pool, table); err != nil {
		return nil, fmt.Errorf("comments-api: %w", err)
	}
	tx := postgres.Transactor{Pool: pool}
	adapter, err := restpostgres.NewAdapter(postgres.NewStore(pool, table), tx, restpostgres.Options{
		Schema: commentsSchema,
		Retry:  rest.DefaultRetryOptions(),
	})
	if err != nil {
		return nil, fmt.Errorf("comments-api: %w", err)
	}
	res := &rest.Resource{
		Name:            "comments",
		ItemTitle:       "comment",
		Schema:          commentsSchema,
		Adapter:         adapter,
		ResourceMethods: []string{http.MethodGet, http.MethodPost},
		ItemMethods:     []string{http.MethodGet, http.MethodPatch, http.MethodDelete},
	}
	api := rest.New(rest.Options{Transactor: tx})
	api.Register(res)
	return api, nil
}
