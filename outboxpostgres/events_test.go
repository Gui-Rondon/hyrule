package outboxpostgres_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/Gui-Rondon/hyrule/behaviors/events"
	"github.com/Gui-Rondon/hyrule/outbox"
	"github.com/Gui-Rondon/hyrule/outboxpostgres"
	"github.com/Gui-Rondon/hyrule/platform/postgres"
	"github.com/Gui-Rondon/hyrule/rest"
	"github.com/Gui-Rondon/hyrule/restpostgres"
)

// TestEventsHooksWorkWithPostgresOutbox proves behaviors/events.Hooks/
// ConditionalHooks' generalization from *outbox.Outbox to the Adder
// interface actually works end to end with outboxpostgres.Outbox, the
// same shape events_test.go's TestEventsFollowTheWrite already proves
// for the Mongo side -- driven through a real restpostgres-backed
// rest.Service, not just a compile check.
func TestEventsHooksWorkWithPostgresOutbox(t *testing.T) {
	pool := testPool(t)
	resourceTable := "events_items_test"
	if _, err := pool.Exec(context.Background(), "DROP TABLE IF EXISTS "+resourceTable); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), `CREATE TABLE `+resourceTable+` (
		id         uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
		doc        jsonb       NOT NULL DEFAULT '{}',
		created_at timestamptz NOT NULL DEFAULT now(),
		updated_at timestamptz NOT NULL DEFAULT now(),
		version    bigint      NOT NULL DEFAULT 1,
		deleted    boolean     NOT NULL DEFAULT false
	)`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pool.Exec(context.Background(), "DROP TABLE IF EXISTS "+resourceTable) })
	if err := postgres.EnsureFunctions(context.Background(), pool); err != nil {
		t.Fatal(err)
	}
	outboxTable := testTable(t, pool)

	box := &outboxpostgres.Outbox{Pool: pool, Table: outboxTable}
	tx := postgres.Transactor{Pool: pool}
	adapter, err := restpostgres.NewAdapter(postgres.NewStore(pool, resourceTable), tx, restpostgres.Options{
		Schema: rest.Schema{"name": {Type: rest.TypeString, MaxLength: 3}},
		Retry:  rest.DefaultRetryOptions(),
	})
	if err != nil {
		t.Fatal(err)
	}
	res := &rest.Resource{
		Name:        "items",
		Adapter:     adapter,
		Schema:      rest.Schema{"name": {Type: rest.TypeString, MaxLength: 3}},
		ItemMethods: []string{http.MethodGet, http.MethodPatch},
	}
	res.Hooks.Use(events.Hooks(box))
	svc := rest.NewService(res, tx)
	ctx := context.Background()
	count := func() int { return countAll(t, pool, outboxTable) }

	// a write without events records nothing
	docs, err := svc.Insert(ctx, []rest.Doc{{"name": "a"}}, rest.WriteOptions{})
	if err != nil || count() != 0 {
		t.Fatalf("insert: %v, events %d", err, count())
	}
	// a Postgres-backed Service's lookup is the raw id value itself
	// (restpostgres/concurrency_test.go's own pattern), not a Mongo-
	// style bson.M{"_id": ...} filter map.
	lookup := docs[0]["_id"]

	ev := outbox.Event{Topic: "t", Key: "k", Body: "1"}
	if _, err := svc.Patch(events.With(ctx, ev), lookup, rest.Doc{"name": "b"}, rest.WriteOptions{SkipConcurrency: true}); err != nil {
		t.Fatal(err)
	}
	if count() != 1 {
		t.Errorf("events after patch = %d", count())
	}

	// an invalid write fails before the hooks; a failing hook rolls back
	if _, err := svc.Patch(events.With(ctx, ev), lookup, rest.Doc{"name": "longo"}, rest.WriteOptions{SkipConcurrency: true}); err == nil {
		t.Error("invalid patch accepted")
	}
	if _, err := svc.Patch(events.With(ctx, outbox.Event{}), lookup, rest.Doc{"name": "c"}, rest.WriteOptions{SkipConcurrency: true}); err == nil {
		t.Error("event without topic accepted")
	}
	doc, _ := svc.Find(ctx, lookup)
	if count() != 1 || doc["name"] != "b" {
		t.Errorf("after failures: events %d, name %v", count(), doc["name"])
	}

	two := events.With(events.With(ctx, ev), outbox.Event{Topic: "u"})
	if _, err := svc.Replace(two, lookup, rest.Doc{"name": "d"}, rest.WriteOptions{SkipConcurrency: true}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Delete(events.With(ctx, ev), lookup, rest.WriteOptions{SkipConcurrency: true}); err != nil {
		t.Fatal(err)
	}
	if count() != 4 {
		t.Errorf("events after replace and delete = %d", count())
	}
}
