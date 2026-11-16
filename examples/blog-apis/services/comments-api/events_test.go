package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"cloud.google.com/go/pubsub/v2"
	"cloud.google.com/go/pubsub/v2/apiv1/pubsubpb"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Gui-Rondon/hyrule/app"
	"github.com/Gui-Rondon/hyrule/outbox"
	"github.com/Gui-Rondon/hyrule/platform/config"
	ps "github.com/Gui-Rondon/hyrule/platform/pubsub"
	"github.com/Gui-Rondon/hyrule/rest"
	"github.com/Gui-Rondon/hyrule/testkit"
)

type commentEventEnv struct {
	c    *testkit.Client
	pool *pgxpool.Pool
	box  string
	t    *testing.T
}

func (e *commentEventEnv) count() int {
	e.t.Helper()
	var n int
	if err := e.pool.QueryRow(context.Background(), "SELECT count(*) FROM "+e.box).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

// Build the production setup, optionally starting its actual registered task.
func eventService(t *testing.T, dry bool, start func(string, func(context.Context) error), fail bool) *commentEventEnv {
	return buildEventService(t, dry, start, fail, "comments-to-publish")
}

func buildEventService(t *testing.T, dry bool, start func(string, func(context.Context) error), fail bool, topic string) *commentEventEnv {
	t.Helper()
	pool := testPool(t)
	if os.Getenv("PUBSUB_EMULATOR_HOST") == "" {
		t.Setenv("PUBSUB_EMULATOR_HOST", "localhost:8085")
	}
	table := "comments"
	box := table + "_outbox"
	t.Cleanup(func() { pool.Exec(context.Background(), "DROP TABLE IF EXISTS "+table+", "+box) })
	setup := setupWithTables(table, box)
	spec := app.Spec{Name: "comments-api", Backend: app.BackendPostgres, Setup: func(ctx context.Context, env *app.Env) error {
		if err := setup(ctx, env); err != nil {
			return err
		}
		if fail {
			// Runs after ConditionalHooks.Add, proving the outbox insert rolls back.
			env.API.Service("comments").Resource().Hooks.Use(rest.Hooks{OnUpdate: []func(context.Context, rest.Doc, rest.Doc) error{func(context.Context, rest.Doc, rest.Doc) error { return errors.New("forced rollback") }}})
		}
		return nil
	}}
	h, err := app.Build(context.Background(), spec, app.Deps{Postgres: pool, Auth: testkit.DisabledAuth(t), DryRun: dry, Start: start,
		Config: config.Config{"pubsub": map[string]any{"project_id": "local-dev", "to_publish": topic, "healthcheck": start != nil}}})
	if err != nil {
		t.Fatal(err)
	}
	return &commentEventEnv{c: testkit.NewClient(t, h), pool: pool, box: box, t: t}
}

func TestCommentPublicationTransitions(t *testing.T) {
	e := eventService(t, false, nil, false)
	c, count := e.c, e.count
	created := c.Do("POST", "/comments", map[string]any{"post_id": "p", "author_name": "Link", "body": "hello"})
	if created.Status != 201 {
		t.Fatalf("create: %d %s", created.Status, created.Raw)
	}
	id, etag := created.String("_id"), created.String("_etag")
	for _, step := range []struct {
		body map[string]any
		want int
	}{
		{map[string]any{"status": "published"}, 1},
		{map[string]any{"body": "edit"}, 1},
		{map[string]any{"status": "published"}, 1},
		{map[string]any{"status": "pending"}, 1},
		{map[string]any{"status": "published"}, 2},
	} {
		r := c.Do("PATCH", "/comments/"+id, step.body, "If-Match", `"`+etag+`"`)
		if r.Status != 200 {
			t.Fatalf("patch: %d %s", r.Status, r.Raw)
		}
		etag = r.String("_etag")
		if n := count(); n != step.want {
			t.Fatalf("events=%d want %d", n, step.want)
		}
	}
	bad := c.Do("PATCH", "/comments/"+id, map[string]any{"status": "pending"}, "If-Match", `"stale"`)
	if bad.Status != 412 || count() != 2 {
		t.Fatalf("stale: %d, events=%d", bad.Status, count())
	}
	invalid := c.Do("POST", "/comments", map[string]any{"status": "published"})
	if invalid.Status != 422 || count() != 2 {
		t.Fatalf("invalid: %d, events=%d", invalid.Status, count())
	}
	inserted := c.Do("POST", "/comments", map[string]any{"post_id": "p", "author_name": "Zelda", "body": "hi", "status": "published"})
	if inserted.Status != 201 || count() != 3 {
		t.Fatalf("insert published: %d, events=%d", inserted.Status, count())
	}
}

func TestCommentDryRunHasNoEventOrWorker(t *testing.T) {
	e := eventService(t, true, func(string, func(context.Context) error) { t.Error("dry run started a task") }, false)
	c, count := e.c, e.count
	r := c.Do("POST", "/comments", map[string]any{"post_id": "p", "author_name": "Link", "body": "hi", "status": "published"})
	if r.Status != 201 || r.Header.Get("X-Dry-Run") != "true" || count() != 0 {
		t.Fatalf("dry run: %d %s events=%d", r.Status, r.Raw, count())
	}
	if got := c.Do("GET", "/comments/"+r.String("_id"), nil); got.Status != 404 {
		t.Fatalf("persisted dry run: %d", got.Status)
	}
}

func TestCommentEventReachesPubSub(t *testing.T) {
	if os.Getenv("PUBSUB_EMULATOR_HOST") == "" {
		t.Skip("PUBSUB_EMULATOR_HOST not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	client, err := ps.Connect(ctx, config.Config{"pubsub": map[string]any{"project_id": "local-dev"}})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	topic := "comments-test-" + uuid.NewString()
	fullTopic := "projects/local-dev/topics/" + topic
	sub := "projects/local-dev/subscriptions/" + topic
	if _, err = client.TopicAdminClient.CreateTopic(ctx, &pubsubpb.Topic{Name: fullTopic}); err != nil {
		t.Fatal(err)
	}
	defer client.TopicAdminClient.DeleteTopic(context.Background(), &pubsubpb.DeleteTopicRequest{Topic: fullTopic})
	if _, err = client.SubscriptionAdminClient.CreateSubscription(ctx, &pubsubpb.Subscription{Name: sub, Topic: fullTopic, EnableMessageOrdering: true}); err != nil {
		t.Fatal(err)
	}
	defer client.SubscriptionAdminClient.DeleteSubscription(context.Background(), &pubsubpb.DeleteSubscriptionRequest{Subscription: sub})
	var wg sync.WaitGroup
	workerCtx, stop := context.WithCancel(ctx)
	// Cleanup precedes helper cleanups, so explicitly stop before returning.
	defer func() { stop(); wg.Wait() }()
	e := buildEventService(t, false, func(_ string, task func(context.Context) error) {
		wg.Go(func() {
			if err := task(workerCtx); err != nil && workerCtx.Err() == nil {
				t.Error(err)
			}
		})
	}, false, topic)
	c, count := e.c, e.count
	r := c.Do("POST", "/comments", map[string]any{"post_id": "p", "author_name": "Link", "body": "hello"})
	if r.Status != http.StatusCreated {
		t.Fatalf("create: %d %s", r.Status, r.Raw)
	}
	id := r.String("_id")
	r = c.Do("PATCH", "/comments/"+id, map[string]any{"status": "published"}, "If-Match", `"`+r.String("_etag")+`"`)
	if r.Status != http.StatusOK {
		t.Fatalf("publish: %d %s", r.Status, r.Raw)
	}
	var received atomic.Bool
	subscriber := client.Subscriber(topic)
	subscriber.ReceiveSettings.MaxOutstandingMessages = 1
	receiveCtx, stopReceive := context.WithCancel(ctx)
	defer stopReceive()
	err = subscriber.Receive(receiveCtx, func(_ context.Context, m *pubsub.Message) {
		var doc map[string]any
		if err := json.Unmarshal(m.Data, &doc); err != nil {
			t.Error(err)
		}
		if doc["_id"] != id || doc["status"] != "published" || doc["post_id"] != "p" || doc["author_name"] != "Link" || doc["body"] != "hello" || m.OrderingKey != id || m.Attributes[outbox.AttrEventType] != "comment.published" || m.Attributes[outbox.AttrEventID] == "" {
			t.Errorf("message: %s %v key=%s", m.Data, m.Attributes, m.OrderingKey)
		}
		if _, err := time.Parse(time.RFC3339Nano, m.Attributes[outbox.AttrOccurredAt]); err != nil {
			t.Error(err)
		}
		received.Store(true)
		m.Ack()
		stopReceive()
	})
	if err != nil || !received.Load() {
		t.Fatalf("receive: %v received=%v", err, received.Load())
	}
	// Pub/Sub reception may precede the relay's SQL acknowledgement.
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		var published bool
		if err := e.pool.QueryRow(ctx, "SELECT published_at IS NOT NULL FROM "+e.box).Scan(&published); err != nil {
			t.Fatal(err)
		}
		if published {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("relay did not acknowledge publication")
		case <-ticker.C:
		}
	}
	stop()
	wg.Wait()
	if count() != 1 {
		t.Fatalf("events=%d", count())
	}
	if hc := c.Do("GET", "/healthcheck", nil); hc.Status != http.StatusOK {
		t.Fatalf("healthcheck: %d %s", hc.Status, hc.Raw)
	}
}

func TestCommentAndEventRollbackTogether(t *testing.T) {
	e := eventService(t, false, nil, true)
	c, count := e.c, e.count
	r := c.Do("POST", "/comments", map[string]any{"post_id": "p", "author_name": "Link", "body": "hello"})
	if r.Status != http.StatusCreated {
		t.Fatalf("create: %d %s", r.Status, r.Raw)
	}
	id := r.String("_id")
	r = c.Do("PATCH", "/comments/"+id, map[string]any{"status": "published"}, "If-Match", `"`+r.String("_etag")+`"`)
	if r.Status < 400 || count() != 0 {
		t.Fatalf("rollback: %d events=%d", r.Status, count())
	}
	if got := c.Do("GET", "/comments/"+id, nil); got.String("status") != "pending" {
		t.Fatalf("comment changed: %s", got.Raw)
	}
}
