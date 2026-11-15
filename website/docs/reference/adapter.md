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

## Prepared plans and an explicit pool policy

The current pgx default caches prepared statements. The adapter binds document
field names as well as values; a generic plan cannot necessarily match those
field parameters to a literal expression index. `auto` can therefore retain
an inefficient plan even after an index is declared. In the local 20,000-row
control, five sorts on an unindexed integer field led the next sort on an
indexed integer field (the same SQL shape) to use a generic Seq Scan + Sort.
A custom plan used the typed index without Sort, with identical results.
This observation does not predict every workload's `auto` choice.

An operator can opt a pool into custom plans through the **existing** connection
URI, without changing compiler SQL or adding an adapter option:

```text
postgres://user:password@host:5432/database?sslmode=require&plan_cache_mode=force_custom_plan
```

Set this on the connection string that actually wins configuration precedence:
`POSTGRES_CONNECTION_STRING`, then `postgres.uri`, then individual connection
settings. Append `&plan_cache_mode=force_custom_plan` when a query string already
exists, or start with `?plan_cache_mode=force_custom_plan`. `postgres.Connect`
passes it to every physical connection as a startup parameter; there is no new
`postgres.plan_cache_mode` configuration key. If the URI omits it, existing
server/role configuration applies (normally `auto`).

This is a **pool-wide opt-in**, including transactions and other users of that
pool, such as writes and outbox queries. Parameterized statements incur planning
work on each execution; an index is still a planner choice, and missing indexes
or unaligned JSONB filters are not repaired by this setting. Parameterless UUID
queries can still use generic plans and their primary key. Measure planning
cost, execution, buffers and application latency against the real workload
before adoption. To roll back, remove the URI option and recreate the pool.
No application default, query transport, binding or production index changes.

`make postgres-ha-plan-policy-test POSTGRES_HA_PORT=5540` is a manual local gate:
five real adapter warmups, a sixth real call on the same connection, and
`EXPLAIN EXECUTE` of **that actual pgx-cached statement**. Twelve plans compare
`auto`, `force_custom_plan` and `force_generic_plan` for equality, datetime,
typed sort and UUID controls. Counter deltas identify the explained plan;
results must match across policies. Two simultaneous `postgres.Connect`
connections also verify URI propagation. Auto choices remain observational;
custom selective/index-order checks are asserted. Artifacts are saved under
`.local/postgres-plan-policy/` after isolated-schema cleanup. The gate provisions
no infrastructure and is not enabled in GitHub Actions.

See PostgreSQL's [prepared plan rules](https://www.postgresql.org/docs/16/sql-prepare.html)
and [plan_cache_mode](https://www.postgresql.org/docs/16/runtime-config-query.html#GUC-PLAN-CACHE-MODE).

## What's still Mongo-only

- `rest.Resource.Datasource.Filter`/`DefaultSort`/`Projection` are
  `bson.M`/`bson.D`-typed — a Postgres resource is assumed, today, to
  never set them (`restpostgres/query.go`'s own doc comment names
  this explicitly).
- A comparison operator (`$gt`/`$lt`/...) in a `where=` filter against
  a declared field needs that field's `Kind` known to
  `platform/postgres`'s compiler — there is no equivalent of Mongo's
  permissive "whatever BSON type is there" filtering.
