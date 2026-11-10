package main

import (
	"context"
	"net"
	"net/http"
	"os"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"

	"github.com/Gui-Rondon/hyrule/internal/outbox"
	"github.com/Gui-Rondon/hyrule/internal/platform/config"
	"github.com/Gui-Rondon/hyrule/internal/platform/redis"
	"github.com/Gui-Rondon/hyrule/internal/testkit"
)

type env struct {
	c    *testkit.Client
	keys *testkit.Keys
	db   *mongo.Database
}

func newService(t *testing.T) *env {
	t.Helper()
	if os.Getenv("PUBSUB_EMULATOR_HOST") == "" {
		// the client is lazy; these tests check the outbox, not the topics
		t.Setenv("PUBSUB_EMULATOR_HOST", "localhost:8085")
	}
	redisHost, redisPort := "127.0.0.1", "1"
	if addr := os.Getenv("REDIS_TEST_ADDR"); addr != "" {
		redisHost, redisPort, _ = net.SplitHostPort(addr)
	}
	cfg := config.Config{
		"mongo":  map[string]any{"collection": "posts"},
		"pubsub": map[string]any{"project_id": "local-dev", "healthcheck": false},
		"redis":  map[string]any{"host": redisHost, "port": redisPort, "socket_timeout": 1},
	}
	keys := testkit.NewKeys(t)
	client, db := testkit.Service(t, spec(), cfg, keys.Authenticator(t))
	client.Header.Set("Authorization", "Basic: "+keys.TokenWith(t, map[string]any{"preferred_username": "writer@example.com"}))
	return &env{c: client, keys: keys, db: db}
}

func (e *env) create(t *testing.T, body map[string]any) (id, etag string) {
	t.Helper()
	res := e.c.Do("POST", "/posts", body)
	if res.Status != http.StatusCreated {
		t.Fatalf("POST = %d %s", res.Status, res.Raw)
	}
	return res.String("_id"), res.String("_etag")
}

func (e *env) raw(t *testing.T, id string) bson.M {
	t.Helper()
	oid, _ := bson.ObjectIDFromHex(id)
	var doc bson.M
	if err := e.db.Collection("posts").FindOne(context.Background(), bson.M{"_id": oid}).Decode(&doc); err != nil {
		t.Fatal(err)
	}
	return doc
}

func (e *env) events(t *testing.T) []bson.M {
	t.Helper()
	cur, err := e.db.Collection(outbox.Collection).Find(context.Background(), bson.M{})
	if err != nil {
		t.Fatal(err)
	}
	var out []bson.M
	cur.All(context.Background(), &out)
	return out
}

func TestCreateDefaultsToDraft(t *testing.T) {
	e := newService(t)
	id, _ := e.create(t, map[string]any{"title": "Hello", "slug": "hello"})
	doc := e.raw(t, id)
	if doc["status"] != "draft" || doc["_created_user"] != "writer@example.com" {
		t.Errorf("insert = %v", doc)
	}
}

func TestLookupBySlug(t *testing.T) {
	e := newService(t)
	e.create(t, map[string]any{"title": "Hello", "slug": "hello-world"})
	res := e.c.Do("GET", "/posts/hello-world", nil)
	if res.Status != http.StatusOK || res.String("title") != "Hello" {
		t.Errorf("GET by slug = %d %s", res.Status, res.Raw)
	}
}

func TestPublish(t *testing.T) {
	e := newService(t)
	id, _ := e.create(t, map[string]any{"title": "Hello", "slug": "hello"})
	res := e.c.Do("POST", "/posts/"+id+"/publish", map[string]any{"title": "Hello"})
	if res.Status != http.StatusOK {
		t.Fatalf("publish = %d %s", res.Status, res.Raw)
	}
	if doc := e.raw(t, id); doc["status"] != "published" || doc["_published"] == nil {
		t.Errorf("published doc = %v", doc)
	}
	events := e.events(t)
	if len(events) != 1 || events[0]["type"] != "post.published" {
		t.Errorf("events = %v", events)
	}
}

func TestSchedule(t *testing.T) {
	e := newService(t)
	id, etag := e.create(t, map[string]any{"title": "Hello", "slug": "hello"})
	res := e.c.Do("POST", "/posts/"+id+"/schedule", map[string]any{"publish_at": "Mon, 02 Jan 2030 15:04:05 GMT"}, "If-Match", etag)
	if res.Status != http.StatusOK {
		t.Fatalf("schedule = %d %s", res.Status, res.Raw)
	}
	if doc := e.raw(t, id); doc["status"] != "scheduled" {
		t.Errorf("scheduled doc = %v", doc)
	}
	events := e.events(t)
	if len(events) != 1 || events[0]["type"] != "post.scheduled" || events[0]["topic"] != topicSchedule {
		t.Errorf("events = %v", events)
	}
}

// The conditional outbox primitive fires from the write alone: a plain
// PATCH that happens to set status to "published" publishes too, without
// going through the /publish endpoint at all.
func TestPlainPatchAlsoPublishes(t *testing.T) {
	e := newService(t)
	id, etag := e.create(t, map[string]any{"title": "Hello", "slug": "hello"})
	res := e.c.Do("PATCH", "/posts/"+id, map[string]any{"status": "published"}, "If-Match", etag)
	if res.Status != http.StatusOK {
		t.Fatalf("patch = %d %s", res.Status, res.Raw)
	}
	events := e.events(t)
	if len(events) != 1 || events[0]["type"] != "post.published" {
		t.Errorf("events = %v", events)
	}
}

func TestScheduleRequiresPublishAt(t *testing.T) {
	e := newService(t)
	id, etag := e.create(t, map[string]any{"title": "Hello", "slug": "hello"})
	res := e.c.Do("POST", "/posts/"+id+"/schedule", map[string]any{}, "If-Match", etag)
	if res.Status != http.StatusUnprocessableEntity {
		t.Errorf("schedule without publish_at = %d %s", res.Status, res.Raw)
	}
}

func TestUnpublish(t *testing.T) {
	e := newService(t)
	id, _ := e.create(t, map[string]any{"title": "Hello", "slug": "hello"})
	if res := e.c.Do("POST", "/posts/"+id+"/publish", nil); res.Status != http.StatusOK {
		t.Fatalf("publish = %d %s", res.Status, res.Raw)
	}
	res := e.c.Do("DELETE", "/posts/"+id+"/publish", nil)
	if res.Status != http.StatusOK {
		t.Fatalf("unpublish = %d %s", res.Status, res.Raw)
	}
	if doc := e.raw(t, id); doc["status"] != "draft" || doc["_unpublished"] == nil {
		t.Errorf("unpublished doc = %v", doc)
	}
	var saw bool
	for _, ev := range e.events(t) {
		if ev["type"] == "post.unpublished" && ev["topic"] == defaultToUnpub {
			saw = true
		}
	}
	if !saw {
		t.Errorf("events missing post.unpublished")
	}
}

func TestUnschedule(t *testing.T) {
	e := newService(t)
	id, etag := e.create(t, map[string]any{"title": "Hello", "slug": "hello"})
	res := e.c.Do("POST", "/posts/"+id+"/schedule", map[string]any{"publish_at": "Mon, 02 Jan 2030 15:04:05 GMT"}, "If-Match", etag)
	if res.Status != http.StatusOK {
		t.Fatalf("schedule = %d %s", res.Status, res.Raw)
	}
	res = e.c.Do("DELETE", "/posts/"+id+"/schedule", nil, "If-Match", res.String("_etag"))
	if res.Status != http.StatusOK {
		t.Fatalf("unschedule = %d %s", res.Status, res.Raw)
	}
	if res.Header.Get("ETag") == "" {
		t.Error("unschedule response missing ETag header")
	}
	if doc := e.raw(t, id); doc["status"] != "draft" {
		t.Errorf("unscheduled doc = %v", doc)
	}
	var saw bool
	for _, ev := range e.events(t) {
		if ev["type"] == "post.unscheduled" && ev["topic"] == topicSchedule {
			saw = true
		}
	}
	if !saw {
		t.Errorf("events missing post.unscheduled")
	}
}

func TestScheduleEtagMismatch(t *testing.T) {
	e := newService(t)
	id, _ := e.create(t, map[string]any{"title": "Hello", "slug": "hello"})
	res := e.c.Do("POST", "/posts/"+id+"/schedule", map[string]any{"publish_at": "Mon, 02 Jan 2030 15:04:05 GMT"}, "If-Match", `"not-the-etag"`)
	if res.Status != http.StatusPreconditionFailed {
		t.Errorf("schedule with wrong etag = %d %s", res.Status, res.Raw)
	}
}

func TestUnscheduleEtagMismatch(t *testing.T) {
	e := newService(t)
	id, _ := e.create(t, map[string]any{"title": "Hello", "slug": "hello"})
	res := e.c.Do("DELETE", "/posts/"+id+"/schedule", nil, "If-Match", `"not-the-etag"`)
	if res.Status != http.StatusPreconditionFailed {
		t.Errorf("unschedule with wrong etag = %d %s", res.Status, res.Raw)
	}
}

func TestScheduleInvalidDate(t *testing.T) {
	e := newService(t)
	id, etag := e.create(t, map[string]any{"title": "Hello", "slug": "hello"})
	res := e.c.Do("POST", "/posts/"+id+"/schedule", map[string]any{"publish_at": "not-a-date"}, "If-Match", etag)
	if res.Status != http.StatusUnprocessableEntity {
		t.Errorf("schedule with invalid date = %d %s", res.Status, res.Raw)
	}
}

func TestScheduleBadJSON(t *testing.T) {
	e := newService(t)
	id, etag := e.create(t, map[string]any{"title": "Hello", "slug": "hello"})
	res := e.c.Do("POST", "/posts/"+id+"/schedule", "not json", "If-Match", etag)
	if res.Status != http.StatusBadRequest {
		t.Errorf("schedule with malformed body = %d %s", res.Status, res.Raw)
	}
}

func TestTargetNotFound(t *testing.T) {
	e := newService(t)
	if res := e.c.Do("POST", "/posts/not-an-object-id/publish", nil); res.Status != http.StatusNotFound {
		t.Errorf("publish with malformed id = %d %s", res.Status, res.Raw)
	}
	if res := e.c.Do("POST", "/posts/"+bson.NewObjectID().Hex()+"/publish", nil); res.Status != http.StatusNotFound {
		t.Errorf("publish with unknown id = %d %s", res.Status, res.Raw)
	}
}

// dispatcher() routes posts-schedule events to the Redis hash instead of
// Pub/Sub; the relay that would normally call it isn't started in tests
// (Env.Go is a no-op outside app.Run), so it's exercised directly here.
func TestDispatcher(t *testing.T) {
	addr := os.Getenv("REDIS_TEST_ADDR")
	if addr == "" {
		t.Skip("REDIS_TEST_ADDR not set")
	}
	host, port, _ := net.SplitHostPort(addr)
	rdb, _, err := redis.Connect(config.Config{"redis": map[string]any{"host": host, "port": port, "socket_timeout": "1"}})
	if err != nil {
		t.Fatal(err)
	}
	defer rdb.Close()
	ctx := context.Background()
	const hash = "test:posts-dispatcher"
	defer rdb.Del(ctx, hash)
	d := dispatcher(rdb, hash, nil)

	t.Run("sets the entry when scheduled", func(t *testing.T) {
		err := d.Publish(ctx, outbox.Message{Topic: topicSchedule, Data: []byte(`{"_id":"p1","publish_at":"2030-01-02T15:04:05"}`)})
		if err != nil {
			t.Fatal(err)
		}
		v, err := rdb.HGet(ctx, hash, "p1").Result()
		if err != nil || v != "2030-01-02T15:04:05" {
			t.Errorf("HGet = %q, %v", v, err)
		}
	})

	t.Run("removes the entry when unscheduled", func(t *testing.T) {
		rdb.HSet(ctx, hash, "p2", "2030-01-02T15:04:05")
		err := d.Publish(ctx, outbox.Message{Topic: topicSchedule, Data: []byte(`{"_id":"p2","publish_at":null}`)})
		if err != nil {
			t.Fatal(err)
		}
		if exists, _ := rdb.HExists(ctx, hash, "p2").Result(); exists {
			t.Error("entry still present after unschedule")
		}
	})

	t.Run("rejects malformed events", func(t *testing.T) {
		if err := d.Publish(ctx, outbox.Message{Topic: topicSchedule, Data: []byte(`not json`)}); err == nil {
			t.Error("expected an error for malformed JSON")
		}
		if err := d.Publish(ctx, outbox.Message{Topic: topicSchedule, Data: []byte(`{"publish_at":"2030-01-02T15:04:05"}`)}); err == nil {
			t.Error("expected an error for a missing _id")
		}
	})
}
