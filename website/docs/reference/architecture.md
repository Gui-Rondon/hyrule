---
sidebar_position: 8
---

# Architecture: the layers and why they exist

```
services (or a YAML, via cmd/hyrule-serve)
    ↓
behaviors   (audit, searchsync, events — reusable hooks)
    ↓
rest   (Resource, Schema, Service, API, Store — the HTTP contract)
    ↓
platform    (mongodb, postgres (scaffold), config, auth, search, pubsub, redis, health, server)
```

Each layer only knows the one below it. `platform` never imports
`rest` or `behaviors`, even when that would save a line — that's how
`internal/platform/mongodb.Store` can satisfy `rest.Store` without
the `mongodb` package knowing `rest` exists (the interface's methods
only use `bson`/stdlib types, not `rest`-owned structs; see
[`store.md`](store.md)). Inverting that direction — `platform` importing
`rest` — would create a structural dependency from a low-level
module to a high-level one even without a real import cycle, and would
confuse any future reader.

A `behaviors/*` package (e.g. `events`, which wires `outbox` into
`rest.Hooks`) can depend on both `rest` and `platform` — that's
exactly what it's there for, to be the glue between the two.

## Decisions that shape the framework

- **Storage is a decision separate from events.** `internal/outbox`
  writes in the same Mongo transaction as the write — a choice of its
  own, not an inevitable consequence of using Mongo. If a resource's
  storage ever changes, the outbox can follow the database (simpler) or
  stay as is (if the new database's transaction doesn't cover the
  outbox the way Mongo's does) — see [`outbox.md`](outbox.md).
- **Storage portability is the interface, not the query language.**
  `rest.Store` decouples the concrete client; filters stay `bson.M`
  and the errors `rest` checks directly stay in the shape the
  Mongo driver returns. A real second backend (`internal/platform/postgres`
  today has only a scaffold, no resource wired to it yet) needs more than
  a new `Store` implementation — see [`store.md`](store.md) and the
  [Postgres RFC](postgres-rfc.md) for what's still open.
- **YAML covers data, not code.** `internal/declarative` expresses
  everything that's already plain data in `rest.Schema`/
  `Field`/`Resource`; `Field.Check`, `Resource.Validate`,
  `Resource.Datasource` and every kind of `Hooks` carry arbitrary
  behavior and are left out on purpose — see
  [`resources-yaml.md`](resources-yaml.md).
