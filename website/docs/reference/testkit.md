---
sidebar_position: 5
---

# Testing a resource

`internal/testkit` sets up what a resource test needs: an isolated
Mongo, an in-process HTTP client, and test tokens. Tests that need Mongo
skip themselves when `MONGO_TEST_URI` isn't set (e.g.
`mongodb://localhost:27018/?directConnection=true`, `make env-up`).

## Booting the whole service

```go
c, db := testkit.Service(t, spec(), config.Config{}, testkit.DisabledAuth(t))

res := c.Do("POST", "/tags", map[string]any{"name": "Go", "slug": "go"})
if res.Status != http.StatusCreated {
	t.Fatalf("POST = %d %s", res.Status, res.Raw)
}
id := res.String("_id")
```

`spec()` is the service's `app.Spec` (the same one `app.Main` uses);
`Service` calls `app.Build` on an isolated database and returns an
in-process HTTP client (`c.Do`, no real port) along with the
`*mongo.Database`, for when the test needs to inspect the stored document
directly:

```go
var doc bson.M
db.Collection("tags").FindOne(ctx, bson.M{"_id": oid}).Decode(&doc)
```

For real authentication, swap `testkit.DisabledAuth(t)` for
`testkit.NewKeys(t).Authenticator(t)`, and sign a token with
`keys.TokenWith(t, claims)`.

## Testing the `Store` in isolation, with no HTTP

To test a hook or a `Resource` without HTTP:

```go
res := &evecompat.Resource{
	Name:  "items",
	Store: testkit.Store(t, "items"), // mongodb.NewStore(testkit.Mongo(t).Collection("items"))
	Schema: evecompat.Schema{"name": {Type: evecompat.TypeString}},
}
svc := evecompat.NewService(res, tx) // tx: see mongodb.TransactorFor
```

`internal/behaviors/events/conditional_test.go` and
`internal/outbox/outbox_test.go` follow this pattern to test hooks and
the outbox with no HTTP and no full service needed.

## Dry run

```go
dryRun := testkit.DryRun(t, spec(), cfg, authn, db) // db: the same one from Service, already seeded
```

Builds the service again, in dry run, on top of the `*mongo.Database`
`Service` already populated — the test prepares data with the real
service and repeats the writes against this second client to confirm
nothing was persisted and no background task (`env.Go`) started (see
[`dry-run.md`](dry-run.md)).

## Elasticsearch and tokens

`testkit.NewES(t)` gives a fake HTTP recorder (`es.Calls()` records what
was indexed, no real Elasticsearch needed) — use `es.Hosts()` in the
resource's config. `testkit.NewKeys(t)` signs test RSA tokens;
`keys.Token(t, "username")` and `keys.TokenWith(t, claims)` cover the two
claim shapes `internal/platform/auth` accepts.
