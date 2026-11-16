---
sidebar_position: 3
---

# Transactional outbox with Mongo and Postgres

Both backends commit the resource write and its pending event in the **same
local database transaction**. The relay publishes after commit, at least once.
Consumers deduplicate by `outbox.AttrEventID` (`event_id`); acknowledgement loss
can publish the same event again. There is no distributed transaction with
Pub/Sub, Redis or another service.

## Mongo integration

```go
box := outbox.New(env.DB)
res.Hooks.Use(events.Hooks(box))
relay := &outbox.Relay{Outbox: box, Publisher: publisher}
env.Go("outbox-relay", relay.Run)
check, err := outbox.CheckFromConfig(env.Config, relay)
if err != nil { return err }
env.AddCheck(check)
```

Use a replica-set transaction. `outbox.New` sets its transaction checker. `relay.Run` ensures pending/TTL
indexes; callers using Drain directly should call `relay.EnsureIndexes(ctx)`
during setup. `Add` rejects a context outside
the write transaction with `outbox.ErrNoTransaction`. `AllowNoTransaction` is
for standalone development and removes atomicity. Mongo relay uses a single
leader lease, ordering pending events through that relay; failures back off
and retry without a built-in dead-letter/replay limit. Published events have
retention/TTL cleanup. See `posts-api` for a complete Mongo integration.

## Postgres integration

```go
if err := outboxpostgres.EnsureTable(ctx, env.Postgres, "outbox"); err != nil { return err }
box := &outboxpostgres.Outbox{Pool: env.Postgres, Table: "outbox"}
res.Hooks.Use(events.Hooks(box))
relay := &outboxpostgres.Relay{Outbox: box, Publisher: publisher, Workers: 1}
env.Go("outbox-relay", relay.Run)
check, err := outbox.CheckFromConfig(env.Config, relay)
if err != nil { return err }
env.AddCheck(check)
```

Use the **same pool** as the resource adapter and `env.PostgresTx`. `Add` uses
the ambient transaction, rejects missing/foreign-pool contexts with the same
`outbox.ErrNoTransaction`, and never silently starts a separate transaction.
`EnsureTable` is setup DDL, not a resource request or a migration engine; on an
existing installation manage incompatible schema changes explicitly.

The Postgres relay claims with `FOR UPDATE SKIP LOCKED`. Multiple workers or
processes can publish concurrently; there is **no cross-worker ordering by
Key**. Default MaxAttempts is 10; exhausted events get `dead_at`, remain stored
and leave the pending scan. `relay.Replay(ctx, id)` requeues only a dead bigint
row ID, clearing retry/dead state; other IDs return `ErrNotReplayable`. Published
rows have no Mongo TTL equivalent: define retention separately. See
`comments-api` for real HTTP, transaction and Pub/Sub delivery tests.

`outbox.CheckFromConfig` works with either relay (`outbox.max_delay`, default
5m). It measures pending backlog age; Postgres dead rows are excluded. Monitor
and operate the dead-event backlog separately rather than treating a green
pending check as proof that no events failed.

## Shared hooks and publishers

Business code can attach an event before a write:

```go
ctx = events.With(ctx, outbox.Event{Topic: "items-changed", Type: "item.changed", Key: id, Body: payload})
// Pass ctx to the service write. events.Hooks flushes it in the pre-write hook.
```

Or register a transition condition for either box:

```go
condition := outbox.OnFieldChange("status", []string{"published"}, func(doc map[string]any) outbox.Event {
    return outbox.Event{Topic: "items-published", Type: "item.published", Key: fmt.Sprint(doc["_id"]), Body: doc}
})
res.Hooks.Use(events.ConditionalHooks(box, condition))
```

Use backend-appropriate ID formatting (ObjectID.Hex for Mongo when required by
your message contract; UUID.String for Postgres). A plain PATCH that makes the
same effective transition triggers the condition too. Do not emit the same
business event through both hook strategies unintentionally.

`publisher` implements `outbox.Publisher`; `outbox.PubSub` and `outbox.Dispatcher`
work with both relays. Provision destination topics/subscriptions separately.
Local dispatcher destinations also run **after commit**: a Redis update is not
atomic with the originating Mongo/Postgres write. A local failure is retried by
the relay, so handlers need idempotency just like remote consumers.

Pre-write hooks must stay in the database transaction. `env.Go` suppresses
relays in dry run; business rows and pending events roll back together. Build
without `Deps.Start` does not start relays, so tests explicitly Drain or run a
worker when checking delivery. [Backend setup](backends.md), [dry run](dry-run.md)
and [testing](testkit.md) cover the surrounding integration.
