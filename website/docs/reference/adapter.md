---
sidebar_position: 1.5
---

# The `Adapter` interface

`rest.Resource.Adapter` (`rest/adapter.go`) is the seam
`rest.Service` writes through for everything a request needs: parsing
and combining `where`/`sort`/`projection`, generating and parsing
ids, CRUD, `MergePatch`, uniqueness checks, and classifying a
backend's errors into the HTTP status they mean. [`store.md`](store.md)'s
`Store` — Mongo-shaped, `bson.M`/`bson.D`-typed — is still exactly
what it always was; `Adapter` sits in front of it, not in its place.
A `Resource` with `Adapter` set never calls `Store` at all
(`Resource.setDefaults`, `rest/resource.go`, only builds a
`mongoAdapter` wrapping `Store` when `Adapter` is `nil` — the default
for a Mongo resource that never had to think about any of this).

This is ADR-0002's revision (see
[`architecture-decisions.md`](architecture-decisions.md)): the
generics rework that ADR deferred stayed deferred; this is the fourth
option it didn't originally consider.

```go
// rest/adapter.go
type Adapter interface {
	QueryAdapter  // where=/sort=/projection=, CombineFilter/Sort/Projection, WriteLookup, IDFilter, RetryPolicy
	IDCodec       // NewID, IDString, LookupByID, LookupID
	ErrorClassifier // IsNotFound, IsDuplicateKey, IsClientError, IsTimeout, ClassifiesWriteTimeouts

	Find(ctx context.Context, filter, sort, projection any, skip, limit int64) ([]Doc, error)
	CountDocuments(ctx context.Context, filter any) (int64, error)
	FindOne(ctx context.Context, lookup, projection any) (Doc, error)
	InsertMany(ctx context.Context, docs []Doc) error
	UpdateOne(ctx context.Context, lookup any, updated, changes Doc) (matched int64, err error)
	ReplaceOne(ctx context.Context, lookup any, replacement Doc) (matched int64, err error)
	DeleteOne(ctx context.Context, lookup any) (deleted int64, err error)

	MergePatch(original, changes Doc) (Doc, error)
	ValidatesMergedDocument() bool
	EqualsFilter(field string, value any, excludeID any) (any, error)
}
```

`QueryAdapter`/`IDCodec`/`ErrorClassifier` were extracted first, as
three separate, independently testable interfaces (mirroring what
`rest/service.go`/`query.go`/`http.go` already did for Mongo, zero
behavior change); `Adapter` embeds all three and adds the write/merge/
uniqueness methods a `Resource` needs to never reach `Store`
directly. `mongoAdapter` (`rest/mongoadapter.go`) and `restpostgres`'s
`postgresAdapter` each implement it in full, with their own
`var _ rest.Adapter = (*...)(nil)` assertion.

## Mongo vs Postgres, side by side

| | `mongoAdapter` | `restpostgres.postgresAdapter` |
|---|---|---|
| Storage shape | one BSON document per `_id` | one row: `id uuid`, `doc jsonb`, `created_at`/`updated_at`/`version`/`deleted` |
| `_etag` | content hash, computed in Go right before the write | the `version` column — `UpdateOne`/`ReplaceOne`'s `RETURNING version` confirms what was actually written, never computed client-side |
| Optimistic concurrency | `_etag` compared in the write filter (CAS), single attempt — `RetryPolicy()` is the zero `RetryOptions` | `version` compared in the `WHERE` (CAS), with a real retry loop under `If-Match: *` (`RetryPolicy()`/`rest.RetryOptions`, Patch's retry budget, seção 9.7 of the portability plan) |
| `MergePatch` | Eve's deep merge (`MERGE_NESTED_DOCUMENTS`) — a literal `null` is stored, never deletes a key | RFC 7396 JSON Merge Patch — `null` deletes the key, replacing the whole value otherwise; `rest.Resource.RequireMergePatchContentType` enforces `application/merge-patch+json` when set |
| `ValidatesMergedDocument()` | `false` — only the client's own PATCH body is validated | `true` — the merged document is checked for `Required` keys RFC 7396 may have deleted (seção 20's Fase 4c) |
| Soft delete | a `_deleted` field inside the document | a physical `deleted` column, never inside `doc` |
| Declared `where=` numbers | `json.Number` on the filter path, converted ad hoc | `platform/postgres`'s `CompileWhere` needs a `Kind` (`KindInteger`/`KindNumber`/`KindDateTime`) per field — see [`querying.md`](querying.md) |
| Error classification | `mongo.ErrNoDocuments`/`IsDuplicateKeyError`/server error codes | Postgres SQLSTATE codes (`23505` unique violation, `57014`/`40P01`/`40001` transient) |

## Building a Postgres-backed resource

`restpostgres.NewAdapter` is what a service registers instead of a
`Store`:

```go
tx := postgres.Transactor{Pool: pool}
adapter, err := restpostgres.NewAdapter(postgres.NewStore(pool, "comments"), tx, restpostgres.Options{
	Schema: schema,                        // the same rest.Schema the Resource declares
	Retry:  rest.DefaultRetryOptions(),
	// SoftDelete: true,                   // only if the Resource is SoftDelete too — nothing keeps the two in sync
})
res := &rest.Resource{
	Name:    "comments",
	Schema:  schema,
	Adapter: adapter,
	// no Store
}
api := rest.New(rest.Options{Transactor: tx}) // the same Transactor — Service's writes and the adapter's own CRUD share one real transaction
api.Register(res)
```

Before the first request, the table needs to exist (`id uuid primary
key default gen_random_uuid()`, `doc jsonb not null default '{}'`,
`created_at`/`updated_at timestamptz`, `version bigint`, `deleted
boolean`) — none of this happens inside `NewAdapter` itself, on
purpose. `platform/postgres.EnsureFunctions` (the `hyrule_safe_*` SQL
functions `KindDateTime` filters/sorts/indexes depend on) and any
declarative index (`platform/postgres.EnsureIndexes`/`IndexSpec`) are
the one exception: a service built with `app.Main`/`app.Spec.Backend:
app.BackendPostgres` gets both called for it automatically at boot
(the same moment `mongo.indexes`/`Spec.Indexes` are for a Mongo-backed
service) — only the table DDL stays the service's own job.
[`examples/blog-apis/services/comments-api`](https://github.com/Gui-Rondon/hyrule/tree/main/examples/blog-apis/services/comments-api)
is a complete, real example doing exactly this — the first (and,
today, only) resource in the repo served end to end over HTTP by a
real binary through this path, instead of only through
`restpostgres`'s own tests.

`app.Main` gained a Postgres side (`app.Spec.Backend`,
`app.Env.Postgres`/`PostgresTx`) in the "postgres em produção" plan's
Streams A/B — `comments-api` uses it (`main.go`): `platform/server`
wiring, config loading, `/status`/`/healthcheck`, and dry run
(`postgres.Transactor.Rollback`) all now work for a Postgres-backed
service exactly as they do for a Mongo one.

## What's still Mongo-only

- `rest.Resource.Datasource.Filter`/`DefaultSort`/`Projection` are
  `bson.M`/`bson.D`-typed — a Postgres resource is assumed, today, to
  never set them (`restpostgres/query.go`'s own doc comment names
  this explicitly).
- A comparison operator (`$gt`/`$lt`/...) in a `where=` filter against
  a declared field needs that field's `Kind` known to
  `platform/postgres`'s compiler — there is no equivalent of Mongo's
  permissive "whatever BSON type is there" filtering.
