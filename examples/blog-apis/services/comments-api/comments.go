package main

import (
	"context"
	"fmt"
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Gui-Rondon/hyrule/app"
	"github.com/Gui-Rondon/hyrule/behaviors/events"
	"github.com/Gui-Rondon/hyrule/outbox"
	"github.com/Gui-Rondon/hyrule/outboxpostgres"
	"github.com/Gui-Rondon/hyrule/platform/health"
	"github.com/Gui-Rondon/hyrule/platform/postgres"
	"github.com/Gui-Rondon/hyrule/platform/pubsub"
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
// tables. Passed as app.Spec.PostgresIndexes (B1): app.Build ensures it
// at boot, the same moment mongo.indexes/Spec.Indexes are for a
// Mongo-backed service.
func commentsIndexes(table string) []postgres.IndexSpec {
	return []postgres.IndexSpec{
		{Table: table, Fields: []postgres.IndexField{{Field: "post_id"}}},
	}
}

// ensureTable creates table if it doesn't exist yet, with the shape
// every restpostgres resource expects (restpostgres/adapter_test.go's
// setupResourceTable: id/doc/created_at/updated_at/version/deleted --
// no migration tool exists yet for a Postgres resource, so a real
// service creates its own table at startup, the same DDL a hand-written
// migration would run once). app.Spec has no table-DDL equivalent of
// Spec.PostgresIndexes, so this stays the service's own job -- unlike
// EnsureFunctions/EnsureIndexes, which app.Build (B1) now runs on its
// own and this Setup must not call a second time.
func ensureTable(ctx context.Context, pool *pgxpool.Pool, table string) error {
	ddl := fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
		id         uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
		doc        jsonb       NOT NULL DEFAULT '{}',
		created_at timestamptz NOT NULL DEFAULT now(),
		updated_at timestamptz NOT NULL DEFAULT now(),
		version    bigint      NOT NULL DEFAULT 1,
		deleted    boolean     NOT NULL DEFAULT false
	)`, table)
	_, err := pool.Exec(ctx, ddl)
	return err
}

// setup wires a real postgresAdapter into env.API -- the app.Main
// counterpart of restpostgres/http_test.go's newHTTPTestAPI, minus the
// test-only table teardown. env.Postgres/env.PostgresTx come from
// app.Build (B1/B2): the same Transactor already wired into env.API's
// Options, so a dry run (DRY_RUN=true) rolls this resource's writes
// back exactly as any Mongo-backed service's would.
//
// Parameterized by table (not just commentsTable) so comments_test.go
// can run the exact same setup against its own throwaway table instead
// of the real "comments" one -- the same table a concurrently running
// e2e run (examples/blog-apis/e2e/run.sh) might be using against the
// same shared dev Postgres.
func setupWithTable(table string) func(ctx context.Context, env *app.Env) error {
	return setupWithTables(table, outboxpostgres.Table)
}

func setupWithTables(table, outboxTable string) func(context.Context, *app.Env) error {
	return func(ctx context.Context, env *app.Env) error {
		if err := ensureTable(ctx, env.Postgres, table); err != nil {
			return fmt.Errorf("comments-api: %w", err)
		}
		adapter, err := restpostgres.NewAdapter(postgres.NewStore(env.Postgres, table), env.PostgresTx, restpostgres.Options{
			Schema: commentsSchema,
			Retry:  rest.DefaultRetryOptions(),
		})
		if err != nil {
			return fmt.Errorf("comments-api: %w", err)
		}
		if err := outboxpostgres.EnsureTable(ctx, env.Postgres, outboxTable); err != nil {
			return err
		}
		box := &outboxpostgres.Outbox{Pool: env.Postgres, Table: outboxTable}
		topic := env.Config.String("pubsub.to_publish", "comments-to-publish")
		relay := &outboxpostgres.Relay{Outbox: box}
		check, err := outbox.CheckFromConfig(env.Config, relay)
		if err != nil {
			return err
		}
		// Validate configuration during setup without retaining a client when
		// Build is used without background tasks (or in dry run).
		ps, err := pubsub.Connect(ctx, env.Config)
		if err != nil {
			return err
		}
		if err := ps.Close(); err != nil {
			return err
		}
		env.AddCheck(check)
		env.AddCheck(health.Checker{Name: "pubsub_healthcheck", Check: func(ctx context.Context) (bool, string) {
			if !env.Config.Bool("pubsub.healthcheck", true) {
				return true, "Healthcheck deactivated"
			}
			client, err := pubsub.Connect(ctx, env.Config)
			if err != nil {
				return false, err.Error()
			}
			defer client.Close()
			return pubsub.Check(env.Config, client, topic).Check(ctx)
		}})
		res := &rest.Resource{
			Name:            "comments",
			ItemTitle:       "comment",
			Schema:          commentsSchema,
			Adapter:         adapter,
			ResourceMethods: []string{http.MethodGet, http.MethodPost},
			ItemMethods:     []string{http.MethodGet, http.MethodPatch, http.MethodDelete},
		}
		res.Hooks.Use(events.ConditionalHooks(box, publishCondition(topic)))
		env.API.Register(res)
		env.Go("outbox-relay", func(ctx context.Context) error {
			client, err := pubsub.Connect(ctx, env.Config)
			if err != nil {
				return err
			}
			defer client.Close()
			pub := &outbox.PubSub{Client: client}
			defer pub.Stop()
			relay.Publisher = pub
			return relay.Run(ctx)
		})
		return nil
	}
}

// setup is the production Setup, over the real "comments" table.
var setup = setupWithTable(commentsTable)

// publishCondition shares the declarative transition semantics of posts-api.
func publishCondition(topic string) outbox.Condition {
	return outbox.OnFieldChange("status", []string{"published"}, func(doc map[string]any) outbox.Event {
		id, _ := doc["_id"].(uuid.UUID)
		return outbox.Event{Topic: topic, Type: "comment.published", Key: id.String(), Body: doc}
	})
}
