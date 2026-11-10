---
sidebar_position: 1
---

# The `Store` interface

`evecompat.Resource.Store` (`internal/evecompat/store.go`) is the only
entry point to data that `evecompat.Service` uses — `Find`,
`CountDocuments`, `FindOne`, `InsertMany`, `UpdateOne`, `ReplaceOne`,
`DeleteOne`. No `*mongo.Collection` appears in
`internal/evecompat/service.go`.

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
res := &evecompat.Resource{Store: store, ...}
```

In a service (`app.Env`), use `env.Store("collection-name")` instead of
building the wrapper by hand.

## What this decouples — and what it doesn't

This interface decides **who talks to the database**, not **the language
the query is written in**: `filter`/`sort`/`update` stay `bson.M`/`bson.D`,
and the errors `evecompat` checks directly (`mongo.ErrNoDocuments`,
`mongo.IsDuplicateKeyError`, timeouts) stay in the shape the Mongo driver
returns. That was an explicit decision: storage portability this round is
the interface, not the query language nor the error semantics.

In practice that means:

- Swapping the *implementation* of `Store` (another Mongo client, an
  instrumentation layer, a cache) is straightforward: satisfy the
  interface.
- Moving from Mongo to another database takes more than implementing
  `Store`: the queries (`bson.M`) and the errors checked in
  `internal/evecompat/service.go` (`queryError`, `duplicateKey`) still
  assume Mongo. A real second backend is the natural next round, not
  something this interface already delivers.

## Places that still go straight to Mongo, on purpose

`internal/outbox` writes to the `outbox` collection with a
`*mongo.Collection` directly — that's its own decision, separate from a
`Resource`'s storage decision (see [`outbox.md`](outbox.md)). A resource
that needs a query `Store` doesn't expose uses `Store` anyway —
`categories-api`'s `GET /categories/tree`
(`examples/blog-apis/services/categories-api/categories.go`) only needs
`Find`/`FindOne` for that — but a more complex aggregation would still
require extending the interface or dropping down to the driver.
