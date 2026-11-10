[Português](pt-br/dry-run.md)

# Dry run: run everything, write nothing

Dry run lets a service take real traffic (writes included) without
actually persisting anything — useful to validate a new version against
production traffic before it gets the final word. It's turned on by the
`DRY_RUN` environment variable (never by the configuration — `app.Build`
refuses to start if it finds a `dry_run` key in the config, on purpose:
it's a deployment switch, not something that could be left on by
accident in a shared config).

```bash
DRY_RUN=1 DRY_RUN_TIMEOUT=10s ./my-api
```

## How it works

- Every write runs inside a Mongo transaction that always rolls back
  (`mongodb.Transactor{Rollback: true}`, built by `app.Build` when
  `DRY_RUN=1`): the client gets the response as if the write happened,
  but nothing lands in the database.
- Post-write hooks (`OnInserted`, `OnUpdated`, `OnReplaced`,
  `OnDeleted`) don't run — they reach systems outside the transaction
  (ES, Pub/Sub...), which have no rollback. See `runPost` in
  `internal/evecompat/service.go`.
- Background tasks registered with `env.Go` (the outbox relay, for
  instance) don't start.
- `DRY_RUN_TIMEOUT` caps every request (default 3s,
  `internal/app/app.go`'s `defaultDryRunTimeout`); without it, a dry run
  could hold the client's next request longer than the real thing would
  take to answer.
- Dry run needs a replica set (no transaction to roll back without one)
  and a `*mongodb.WriteGuard` on the Mongo client — `app.Run` builds
  both; whoever calls `app.Build` directly (tests) needs to pass both in.

## The write-outside-a-transaction tripwire

Not every write goes through the normal `evecompat.Service` path — a
custom endpoint can write straight to the `Store`. If that write isn't
inside a transaction, the automatic rollback never reaches it: it would
actually be stored, even in dry run.

`mongodb.WriteGuard` (`internal/platform/mongodb/guard.go`) is a driver
command monitor: it can't stop the write (the driver gives no such
hook), but it counts and logs every write it sees outside a transaction.
`/healthcheck` fails permanently from the first violation on
(`dryRunCheck` in `internal/app/dryrun.go`) — it stays red until the pod
restarts, but `/status` keeps answering, so the pod doesn't drop out of
rotation because of it.

## Routes with no way back

A route whose effect leaves Mongo and has no rollback (an HTTP call to
another service, for instance) uses `Env.RefuseInDryRun`:

```go
env.API.Handle("POST /posts/{id}/notify", env.RefuseInDryRun(
	"calls another service, no rollback", http.HandlerFunc(e.notify)))
```

In dry run that answers 503 with the reason, instead of running.
