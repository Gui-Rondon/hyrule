---
sidebar_position: 1
---

# The `Store` interface

`rest.Resource.Store` (`internal/rest/store.go`) is the only
entry point to data that `rest.Service` uses — `Find`,
`CountDocuments`, `FindOne`, `InsertMany`, `UpdateOne`, `ReplaceOne`,
`DeleteOne`. No `*mongo.Collection` appears in
`internal/rest/service.go`.

```go
type Store interface {
	Find(ctx context.Context, filter bson.M, skip, limit int64, sort bson.D, projection bson.M) ([]Doc, error)
	CountDocuments(ctx context.Context, filter bson.M) (int64, error)
	FindOne(ctx context.Context, filter bson.M, projection bson.M) (Doc, error)
	InsertMany(ctx context.Context, docs []any) error
	UpdateOne(ctx context.Context, filter, update bson.M) (matched int64, err error)
	ReplaceOne(ctx context.Context, filter bson.M, replacement any) (matched int64, err error)
	DeleteOne(ctx context.Context, filter bson.M) (deleted int64, err error)
}
```

The only implementation today is `internal/platform/mongodb.Store`, a thin
wrapper around `*mongo.Collection`:

```go
store := mongodb.NewStore(db.Collection("tags"))
res := &rest.Resource{Store: store, ...}
```

In a service (`app.Env`), use `env.Store("collection-name")` instead of
building the wrapper by hand.

`internal/platform/postgres.Store` follows the same `NewStore`-wraps-a-
client, `Find`/`FindOne`/... shape, for a relational database — it just
isn't an implementation of the interface above (see
[Postgres backend](postgres-rfc.md) for why), so nothing here wires it
to a `rest.Resource` yet:

```go
store := postgres.NewStore(pool, "tags")
docs, err := store.Find(ctx, postgres.Filter{"status": "draft"}, 0, 10, nil, nil)
```

## What this decouples — and what it doesn't

This interface decides **who talks to the database**, not **the language
the query is written in**: `filter`/`sort`/`update` stay `bson.M`/`bson.D`,
and the errors `rest` checks directly (`mongo.ErrNoDocuments`,
`mongo.IsDuplicateKeyError`, timeouts) stay in the shape the Mongo driver
returns. That was an explicit decision: storage portability this round is
the interface, not the query language nor the error semantics.

In practice that means:

- Swapping the *implementation* of `Store` (another Mongo client, an
  instrumentation layer, a cache) is straightforward: satisfy the
  interface.
- Moving from Mongo to another database takes more than implementing
  `Store`: the queries (`bson.M`) and the errors checked in
  `internal/rest/service.go` (`queryError`, `duplicateKey`) still
  assume Mongo. `internal/platform/postgres` has a first scaffold (its
  own `Store`-shaped type, not an implementation of this interface —
  see [Postgres backend](postgres-rfc.md) for why and for what's still
  open), but no resource is wired to it yet.

## Query performance today

`where=` (see [`querying.md`](querying.md) for the filter syntax itself)
has no cost model or `explain()` exposed — the only lever is the index
declared for the collection it hits. Indexes are declared per service in
an `indices.conf` file, or added/overridden via the `mongo.indexes` config
key, and built at startup by `internal/platform/mongodb.EnsureIndexes`
(`mongo.indexes_mode`: `create` builds them, `check` only logs what's
missing, `off` skips this entirely):

```
# collection  keys                               options
settings      product:1                          unique
articles      classification_id:1,_updated:-1    name=by_classification
articles      friendly_title:1                   unique sparse
sessions      created:1                          ttl=3600
```

Keys are `field:direction`, direction one of `1`, `-1`, `text`, `hashed`,
`2dsphere`; options are `unique`, `sparse`, `name=`, `ttl=` (seconds). A
`where=` filter with no matching index degenerates into a full collection
scan — there is no query planner feedback surfaced today, so declaring
the right index is the whole optimization story for a Mongo-backed
resource.

This is Mongo-shaped end to end (the index directions above, `ttl=`, the
`indices.conf` format itself) and won't carry over to Postgres as-is:
Postgres' default index is a btree, with `gin` for `jsonb`/array/full-text
columns, `gist` for geometric/range types and `brin` for large,
naturally-ordered tables; there's no native `ttl` (it needs a cron job or
trigger), and `EXPLAIN ANALYZE` gives a real query plan where Mongo's
`where=` gives none. See [Postgres backend](postgres-rfc.md) for where
that's headed.

## Places that still go straight to Mongo, on purpose

`internal/outbox` writes to the `outbox` collection with a
`*mongo.Collection` directly — that's its own decision, separate from a
`Resource`'s storage decision (see [`outbox.md`](outbox.md)). A resource
that needs a query `Store` doesn't expose uses `Store` anyway —
`categories-api`'s `GET /categories/tree`
(`examples/blog-apis/services/categories-api/categories.go`) only needs
`Find`/`FindOne` for that — but a more complex aggregation would still
require extending the interface or dropping down to the driver.
