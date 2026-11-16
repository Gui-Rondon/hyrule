---
sidebar_position: 8
---

# Architecture: the layers and why they exist

```
services (or a YAML, via cmd/hyrule-serve)
    ↓
behaviors   (audit, searchsync, events — reusable hooks)
    ↓
rest   (Resource, Schema, Service, API, Adapter — the HTTP contract)
    ↓
platform    (mongodb, postgres, config, auth, search, pubsub, redis, health, server)
```

Each layer only knows the one below it. `platform` never imports
`rest` or `behaviors`, even when that would save a line — that's how
`platform/mongodb.Store` can satisfy `rest.Store` without
the `mongodb` package knowing `rest` exists (the interface's methods
only use `bson`/stdlib types, not `rest`-owned structs; see
[`store.md`](store.md)). Inverting that direction — `platform` importing
`rest` — would create a structural dependency from a low-level
module to a high-level one even without a real import cycle, and would
confuse any future reader.

One exception, by design: `restpostgres` is the single package
allowed to import both `rest` and `platform/postgres` — it's the
bridge, not a new layer, implementing `rest.Adapter`
(see [`adapter.md`](adapter.md)) the same way `mongoAdapter` does
inside `rest` itself for `platform/mongodb`. A `Resource` picks one of
the two by setting `Adapter` (`restpostgres.NewAdapter(...)`) or
`Store` (`platform/mongodb.NewStore(...)`, which `rest` wraps in a
`mongoAdapter` automatically) — never both.

A `behaviors/*` package (e.g. `events`, which wires `outbox` into
`rest.Hooks`) can depend on both `rest` and `platform` — that's
exactly what it's there for, to be the glue between the two.

## Decisions that shape the framework

- **Storage and events share a local transaction.** Mongo resources use
  `outbox`; Postgres resources use `outboxpostgres` with the same pool/context.
  Shared hooks depend on the Adder contract, but atomicity never spans two
  databases or an external publisher. See [outbox](outbox.md).
- **Storage portability is the interface, not the query language.**
  `rest.Store` stayed exactly `bson.M`/`bson.D`-typed, Mongo-shaped,
  never generified (ADR-0002) — a real second backend needed a
  different seam in front of it, not a new `Store` implementation:
  `rest.Adapter` (see [`adapter.md`](adapter.md)), with `restpostgres`
  implementing it over `platform/postgres` for the first real
  Postgres-backed resource (`examples/blog-apis/services/comments-api`).
  Each backend still keeps its own query language and error semantics
  — `where=`/`sort=` stay `bson.M`/`bson.D` for Mongo and a
  `postgres.Query` compiled by `platform/postgres` for Postgres, never
  a shared filter AST.
- **YAML covers data, not code.** `declarative` expresses
  everything that's already plain data in `rest.Schema`/
  `Field`/`Resource`; `Field.Check`, `Resource.Validate`,
  `Resource.Datasource` and every kind of `Hooks` carry arbitrary
  behavior and are left out on purpose — see
  [`resources-yaml.md`](resources-yaml.md).

See [both backend integrations](backends.md) and the [review/evidence](integration-review.md) for current bootstrap behavior and limits.
