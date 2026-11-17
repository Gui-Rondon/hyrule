---
sidebar_position: 5
---

# Dry run with Mongo and Postgres

`DRY_RUN=1` runs requests with database writes rolled back. It is a deployment
switch, not shared configuration: `app.Build` rejects a `dry_run` config key.
Both `app.BackendMongo` and `app.BackendPostgres` support it.

```sh
DRY_RUN=1 DRY_RUN_TIMEOUT=10s ./my-api
```

## Mongo integration

`app.Run` installs `mongodb.Transactor{Rollback: true}` and a
`mongodb.WriteGuard` command monitor. A replica set is required; standalone
Mongo cannot roll writes back. Direct `app.Build` callers supply `Mongo`, `DB`,
`DryRun: true` and `WriteGuard` on the monitored client (see `testkit.DryRun`).
The monitor records writes outside transactions; it does not block them.
After a violation `/healthcheck` remains failed until restart, while `/status`
continues answering. Custom writes must still join the request transaction.

## Postgres integration

`app.Build` sets `env.PostgresTx` to `postgres.Transactor{Pool: pool,
Rollback: true}`. Pass that same value to `restpostgres.NewAdapter`. Custom
writes in pre-write hooks should obtain SQL access with
`env.PostgresTx.TransactionQuerier(ctx)`. It rejects missing transactions
and transactions on another pool. For a custom route, open the transaction
with `env.PostgresTx.WithTransaction` and use its callback context. Use `Deps{Postgres: pool,
DryRun: true}` when building in tests; no Mongo client/WriteGuard is required.
A raw `pool.Exec` bypasses the transaction and can persist writes: Postgres
has no WriteGuard-equivalent to detect it. Dry run protects the transactional
request path, not arbitrary database calls or administrative DDL.

## Shared behavior and setup limits

- The client receives the response from the attempted write; business data
  and transactional outbox inserts are rolled back together. A later request
  does not see those attempted changes.
- Post-write hooks (`OnInserted`, `OnUpdated`, `OnReplaced`, `OnDeleted`) are
  suppressed. Pre-write hooks still execute and must limit effects to the
  transaction. `env.Go` background tasks (including relays) are not started.
- `DRY_RUN_TIMEOUT` caps each request (default 3s). HTTP/database timeouts do
  not undo external effects; [outbox](outbox.md) is the delivery boundary.
- Declared indexes use check instead of create, unless mode is already off.
  Postgres dry-run startup skips installing the shared SQL helpers. Run
  `postgres.EnsureFunctions` during provisioning or start in normal mode
  before mirroring traffic. Provision resource and outbox tables in advance.
  Application Setup still runs and must use `env.DryRun` to avoid DDL and
  irreversible effects.
- Postgres sequences/identity allocation may leave gaps even after rollback.
  Dry run promises no committed transactional business rows, not unchanged
  server metadata or counters.
- `Spec.NoDryRun` refuses a service that cannot safely mirror traffic.

A route that calls another service or has an effect without rollback uses:

```go
env.API.Handle("POST /items/{id}/notify", env.RefuseInDryRun(
    "calls another service, no rollback", http.HandlerFunc(notify)))
```

It answers 503 with the reason in dry run. See [backend setup](backends.md)
and [real database tests](testkit.md) for both registration paths.
