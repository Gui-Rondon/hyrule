---
sidebar_position: 6
---

# Testing Mongo and Postgres resources

`testkit` sets up what a resource test needs: an isolated
Mongo database, an in-process HTTP client, and test tokens. The HTTP/token
helpers work with either backend; Service/Mongo/Store/DryRun provision Mongo.
For Postgres, supply an isolated real pool to app.Build as shown below. Tests that need Mongo
skip themselves when `MONGO_TEST_URI` isn't set (e.g.
`mongodb://localhost:27018/?directConnection=true`, `make env-up`).

## Mongo: booting the whole service

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
res := &rest.Resource{
	Name:  "items",
	Store: testkit.Store(t, "items"), // mongodb.NewStore(testkit.Mongo(t).Collection("items"))
	Schema: rest.Schema{"name": {Type: rest.TypeString}},
}
svc := rest.NewService(res, tx) // tx: see mongodb.TransactorFor
```

`behaviors/events/conditional_test.go` and
`outbox/outbox_test.go` follow this pattern to test hooks and
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
claim shapes `platform/auth` accepts.


## Postgres: booting the whole service

Use `POSTGRES_TEST_URI` (local dev: `postgres://postgres:postgres@localhost:5433/postgres?sslmode=disable`).
`testkit.Service` is Mongo-specific; use `app.Build` and `testkit.NewClient`
with a real isolated Postgres schema. Do not reuse a production schema or run
DDL against a shared resource table. This pattern needs imports from pgx/v5,
pgxpool, google/uuid, app, config, testkit and the standard library:

```go
// spec() is your Postgres app.Spec, registering table "items".
ctx := context.Background()
uri := os.Getenv("POSTGRES_TEST_URI")
if uri == "" { t.Skip("POSTGRES_TEST_URI not set") }
admin, err := pgxpool.New(ctx, uri)
if err != nil { t.Fatal(err) }
t.Cleanup(admin.Close)
schemaName := "t_" + strings.ReplaceAll(uuid.NewString(), "-", "")
quoted := pgx.Identifier{schemaName}.Sanitize()
if _, err := admin.Exec(ctx, "CREATE SCHEMA " + quoted); err != nil { t.Fatal(err) }
t.Cleanup(func() {
    cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
    defer cancel()
    if _, err := admin.Exec(cleanup, "DROP SCHEMA " + quoted + " CASCADE"); err != nil { t.Error(err) }
})
cfg := admin.Config()
cfg.ConnConfig.RuntimeParams["search_path"] = schemaName
pool, err := pgxpool.NewWithConfig(ctx, cfg)
if err != nil { t.Fatal(err) }
t.Cleanup(pool.Close)
// Apply your real resource migration to this schema before Build.
handler, err := app.Build(ctx, spec(), app.Deps{
    Postgres: pool, Config: config.Config{}, Auth: testkit.DisabledAuth(t),
})
if err != nil { t.Fatal(err) }
c := testkit.NewClient(t, handler)
res := c.Do("POST", "/items", map[string]any{"name": "Go"})
if res.Status != http.StatusCreated { t.Fatalf("POST = %d %s", res.Status, res.Raw) }
```

The pool is closed before the isolated schema is dropped. Apply the same
migrations used by the service, including outbox tables if needed. Reuse
`env.PostgresTx` in Setup; app.Build installs shared functions and ensures
`Spec.PostgresIndexes`. Build does not own the injected pool and does not run
workers without `Deps.Start`. Use `httptest.NewServer(handler)` only when a real
HTTP socket is required. See the concrete tests in `app/backend_contract_test.go`
and `examples/blog-apis/services/comments-api/migrations_test.go`.

For Postgres dry run, call app.Build again with the **same seeded pool** and
`Deps{Postgres: pool, DryRun: true, Auth: authn}`. Use testkit.NewClient on that
handler, perform writes, then inspect data through the normal client/pool.
No Mongo WriteGuard is required. Test rollback of both resource and outbox rows;
raw pool writes and setup DDL are outside that protection.

## Validation commands for both backends

```sh
make env-up
make test GOFLAGS='-count=1 -p=1'
make vet
make cover GOFLAGS=-p=1
make standalone-example-test
# After tests/coverage finish (avoid concurrent Docker builds):
make e2e
```

The full suite reads both MONGO_TEST_URI and POSTGRES_TEST_URI from Makefile.
Without those variables, database tests skip; a green no-infrastructure run is
not integration evidence. Package concurrency is limited above because a real
retry-budget test is sensitive to CPU contention. Coverage excludes manual HA
gates unless explicitly enabled: see [gate evidence](integration-review.md).
