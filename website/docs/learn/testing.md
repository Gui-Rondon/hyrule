---
sidebar_position: 6
---

# Testing

Everything so far has been exercised with `curl`. This page writes the
same kind of check as a Go test, using `internal/testkit` — the package
every resource in hyrule, including the framework's own test suite,
tests with.

:::info[You will learn]
- How to boot a whole service against an isolated MongoDB
- How to assert on the stored document, not just the HTTP response
- How to test a hook without any HTTP at all
:::

## Booting the service

`testkit.Service` builds a resource's `app.Spec` (the same one its
`main.go` passes to `app.Main`) against a throwaway database, and hands
back an in-process HTTP client:

```go
func newService(t *testing.T) *env {
	keys := testkit.NewKeys(t)
	client, db := testkit.Service(t, spec(), cfg, keys.Authenticator(t))
	client.Header.Set("Authorization", "Basic: "+keys.TokenWith(t, map[string]any{"preferred_username": "writer@example.com"}))
	return &env{c: client, db: db}
}
```

This is `examples/blog-apis/services/posts-api/posts_test.go`'s setup,
trimmed. `testkit.Service` doesn't start a real network listener —
`client.Do(method, path, body)` calls the handler directly, so a test
runs in milliseconds and needs no port.

## Asserting on the stored document

The HTTP response to a write only carries the meta fields (`_id`,
`_etag`, ...) — to check what actually got stored, read the collection
directly with the `*mongo.Database` `Service` also returns:

```go
func TestPublish(t *testing.T) {
	e := newService(t)
	id, _ := e.create(t, map[string]any{"title": "Hello", "slug": "hello"})

	res := e.c.Do("POST", "/posts/"+id+"/publish", map[string]any{})
	if res.Status != http.StatusOK {
		t.Fatalf("publish = %d %s", res.Status, res.Raw)
	}

	var doc bson.M
	oid, _ := bson.ObjectIDFromHex(id)
	e.db.Collection("posts").FindOne(context.Background(), bson.M{"_id": oid}).Decode(&doc)
	if doc["status"] != "published" {
		t.Errorf("status = %v", doc["status"])
	}
}
```

The outbox collection is just as reachable — `e.db.Collection(outbox.Collection)`
— which is how [the previous page's](./the-outbox.md) claim that
`/publish` and a plain `PATCH` fire the same event actually gets
verified, in `TestPlainPatchAlsoPublishes`.

## Testing a hook without HTTP

Not every test needs a running service. `internal/behaviors/events`'s
own test for `events.ConditionalHooks` builds a bare `evecompat.Service`
around a `Resource` and drives it directly:

```go
db := testkit.Mongo(t)
tx, replicaSet, _ := mongodb.TransactorFor(context.Background(), db.Client())
if !replicaSet {
	t.Skip("Mongo is not a replica set")
}

res := &evecompat.Resource{
	Name:   "items",
	Store:  testkit.Store(t, "items"),
	Schema: evecompat.Schema{"status": {Type: evecompat.TypeString}},
}
res.Hooks.Use(events.ConditionalHooks(box, cond))
svc := evecompat.NewService(res, tx)

svc.Insert(ctx, []evecompat.Doc{{"status": "draft"}}, evecompat.WriteOptions{})
// ... svc.Patch, svc.Replace, svc.Delete — same Service API a custom
// endpoint uses, no HTTP envelope around it.
```

`testkit.Store(t, "items")` is the one-line version of
`mongodb.NewStore(testkit.Mongo(t).Collection("items"))` — the same
`Store` interface from [Your First Resource](./your-first-resource.md),
pointed at a database that gets dropped when the test ends.

Tests that need Mongo skip themselves when `MONGO_TEST_URI` isn't set, so
`go test ./...` still runs everywhere — `make env-up` followed by
`make test` is what runs all of them for real.

:::tip[Recap]
- `testkit.Service` boots a whole resource against an isolated Mongo and
  gives you an in-process HTTP client — no real port, no slow tests.
- Read the collection directly (`e.db.Collection(...)`) to assert on what
  was actually stored, not just the HTTP response.
- `testkit.Store` + `evecompat.NewService` test a `Resource`'s hooks
  with no HTTP at all.
- Next: [Going Further](./going-further.md) — when to reach for Go
  instead of YAML, and where to go from here.
:::
