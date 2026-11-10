---
sidebar_position: 9
---

# Adding a Postgres backend

This page is a design document, not a feature reference: it records the
open questions for a second, non-Mongo backend, and a recommendation for
each — not something already built. Today there is a scaffold
(`internal/platform/postgres`: a `Store`-shaped type with its own filter
type, proven to compile and follow the same pattern as
`internal/platform/mongodb`) and nothing wired to `rest` yet. Treat
every "recommended" below as a starting position for discussion, not a
decision already made; the two points marked open are explicitly not
decided.

## Why this exists

[`store.md`](store.md) and [`architecture.md`](architecture.md) already
say, explicitly, that today's storage portability is the `Store`
*interface*, not the query language or the error semantics:
`rest.Store`'s methods are typed in `bson.M`/`bson.D`, and
`rest.Service`'s error translation (`queryError`, `duplicateKey` in
`internal/rest/service.go`) checks Mongo driver types directly
(`mongo.IsDuplicateKeyError`, `mongo.ServerError` codes, `mongo.IsTimeout`).
That was a deliberate choice for the first round, not an oversight — this
page is the "next round" those pages point to.

## 1. The shape of a Postgres `Store`

**Decided.** `internal/platform/postgres.Store` does not try to satisfy
`rest.Store` — that interface is intentionally Mongo-shaped. Instead it
has its own method set, patterned after it (`Find`, `CountDocuments`,
`FindOne`, `InsertMany`, `UpdateOne`, `ReplaceOne`, `DeleteOne`), with
Postgres-native types for the filter/update instead of `bson.M`/`bson.D`.
This mirrors how `mongodb.Store` already "structurally satisfies"
`rest.Store` without importing `rest` — the same pattern, a different
package, with its own types.

Explicitly **not** pursued: a shared filter AST that both `mongodb.Store`
and `postgres.Store` compile from. `where=` stays Mongo's query language
as-is; a Postgres-backed resource defines its own filtering convention,
on its own schedule — not a translation of Mongo operators into SQL.

## 2. How a resource would actually use it — open

This is the question the interface split above doesn't answer:
`rest.Resource.Store` is typed to the Mongo-shaped `Store` interface
today, so a Postgres `Store` can't just be swapped in. Two paths, neither
chosen yet:

- **Bypass `rest` entirely (lower cost, recommended starting point).** A
  Postgres-backed resource talks to `platform/postgres.Store` directly
  from its own HTTP handler, the way `internal/outbox` already talks to
  `*mongo.Collection` directly, on purpose, bypassing `rest.Store`
  (`architecture.md`: "Storage is a decision separate from events").
  It gets none of `rest`'s machinery for free — no `where=`, no Eve-style
  pagination/links/etags — and defines its own query string convention.
- **Generify `rest.Store`/`Service`/`Resource` (bigger lift, not
  attempted yet).** Go generics (`Store[F, U any]`) could let `rest`
  keep driving pagination/links/etags for both backends, with each
  backend supplying its own filter type `F` and update type `U`. This
  touches most of `internal/rest/service.go` and forces a decision on
  what else has to become backend-pluggable — starting with point 3
  below, since `queryError`/`duplicateKey` can't stay hardcoded to Mongo
  error types once a second backend is real.

## 3. Errors

`internal/rest/service.go`'s `queryError`/`duplicateKey` check Mongo
driver types directly today. A Postgres equivalent would check
`*pgconn.PgError`:

| Mongo today | Postgres equivalent | Meaning |
|---|---|---|
| `mongo.IsDuplicateKeyError` | `Code == "23505"` (`unique_violation`) | → 409 on insert, 400 on update/replace, same as `duplicateKey` does today |
| error codes `2`, `9`, `51091`, `51108` (`clientQueryErrors`) | `22P02` (`invalid_text_representation`), `42601` (`syntax_error`), and similar | → 400, client-caused |
| `mongo.IsTimeout` / `context.DeadlineExceeded` | statement/context timeout | → 503, same as today |

Not pursued yet: a shared error-classification interface across
backends. Point 2 above has to land first — there's no single place to
hang it on until then.

## 4. Indexes

`internal/platform/mongodb`'s `IndexSpec`/`indices.conf` format
(directions `1`, `-1`, `text`, `hashed`, `2dsphere`; options `unique`,
`sparse`, `name=`, `ttl=`) is Mongo-shaped end to end — see
[Query performance today](store.md#query-performance-today). A Postgres
equivalent needs its own format, not a reuse of this parser: index
*type* (`btree` default, `gin` for `jsonb`/array/full-text, `gist` for
geometric/range types, `brin` for large naturally-ordered tables) instead
of direction, plus partial and expression indexes, which have no Mongo
equivalent at all. No native `ttl` — Postgres needs a cron job or trigger
for expiry.

## 5. Shape of the data — open, no recommendation yet

Mongo stores schemaless documents; `rest.Doc` is `map[string]any`
everywhere in `rest`/`declarative`. Postgres is relational, and there
are two genuinely different ways to land on it, registered here without
a recommendation:

- **Columnar.** A resource's `rest.Schema`/declarative YAML would need
  to generate real table DDL (and migrations). Gets native types and
  indexing, the actual reason to reach for Postgres — but gives up the
  schemaless `Doc` assumption baked into `rest`/`internal/declarative`
  today.
- **A single `jsonb` column.** Nearly zero translation — `Doc =
  map[string]any` maps onto `jsonb` almost directly — but weakens the
  case for Postgres in the first place: you'd mostly be buying
  transactional/relational guarantees, not typed columns or real
  indexing.

## 6. Aggregations — not a parity gap

Unlike Mongo, Postgres doesn't need an aggregation-pipeline escape hatch
the way `categories-api`'s `GET /categories/tree` needs one today (see
[`store.md`](store.md#places-that-still-go-straight-to-mongo-on-purpose)):
joins, window functions and CTEs are native SQL. Recorded here so it
isn't mistaken for a missing feature later — it's a difference in kind,
not something Postgres support still owes.

## 7. Driver

**Recommended:** [`github.com/jackc/pgx/v5`](https://github.com/jackc/pgx)
(`pgxpool` for pooling). It matches the project's existing pattern of a
direct, idiomatic driver with no ORM in between (`mongo-driver/v2`,
`go-redis/v9`), and `pgconn.PgError` gives the error codes in point 3
natively, without a second parsing layer. It also satisfies everything
points 8 and 9 below need — `LISTEN`/`NOTIFY`, `FOR UPDATE SKIP LOCKED`,
`COPY`, batching, pool tuning — none of it requires a different driver
or a second library.

## 8. Security: SQL injection and beyond

Today's scaffold has no raw-SQL-from-untrusted-input problem: every
value reaches the database as a query parameter (`$N`), never built into
the query text, and every identifier — table and column names — goes
through `pgx.Identifier{...}.Sanitize()`, the same technique as
Postgres' own `quote_ident()`, before being concatenated into SQL.
There's no path from a `Filter`/`SortField`/column-list value to raw SQL
text today.

That holds because today's only caller is Go code written by whoever
wires up the `Store` — the same trust model `mongodb.Store` has. It
stops holding the moment point 2 is resolved and an HTTP request's
`sort=`/`projection=`-equivalent query string, or a write's body, feeds
`SortField.Column`, the `columns []string` argument, or `Filter`'s keys
directly. `Sanitize()` still prevents *injection* in that case — a field
named `x"; DROP TABLE y; --` renders as a syntactically safe, useless
column reference, not executable SQL — but it doesn't prevent
*unauthorized access*: nothing stops a client from asking to sort by, or
read, a column the resource's Schema never meant to expose. Whatever
bridges HTTP to `postgres.Store` needs to validate every client-supplied
column name against an allowlist — the resource's declared Schema —
before it reaches `Store`, the same job `rest` already does for Mongo by
rejecting unknown fields during validation. Syntactic sanitization and
authorization are two different jobs; today's code only does the first,
because nothing calls it with untrusted column names yet.

Two more gaps a production Postgres backend has that the scaffold
doesn't close yet:

- **A query timeout.** Nothing sets `statement_timeout` today, so a
  pathological filter can hold a pool connection indefinitely —
  `IsTimeout` already anticipates the resulting error (code `57014`),
  but nothing produces it yet. `pgxpool.Config.ConnConfig.RuntimeParams`
  (or a per-call `SET LOCAL statement_timeout`) closes this.
- **A connection string that enforces TLS appropriately for the
  environment** (`sslmode=require`/`verify-full` outside local dev) —
  the same operational question `mongodb`'s connection setup already
  answers, just not yet written down for Postgres (see point 9's
  `Connect` gap).

A future, richer filter language (point 1 isn't decided) carries the
same rule forward: whatever syntax replaces equality-only `Filter`,
every value it accepts — including a `LIKE` pattern — must still become
a bound parameter, never text spliced into the query. It's the same
posture Mongo's own `where=` takes with its operator denylist (`$where`,
`$function`, `$accumulator` block code execution, not just bad syntax —
see [`querying.md`](querying.md)).

## 9. Beyond parity: what Postgres can do that this scaffold doesn't use yet

Everything above is about reaching the shape Mongo's `Store` already
has. Postgres also has capabilities this framework's Mongo side doesn't
— not required for parity, but worth naming so a future round doesn't
have to rediscover them. `pgx/v5` already covers all of it; none of this
needs a different driver.

- **A transactional outbox that doesn't need a separate transaction
  mechanism.** `internal/outbox` writes to Mongo's `outbox` collection in
  the same multi-document transaction as the resource write — a real
  feature, but one Mongo needs a replica set and an explicit
  `WithTransaction` for. In Postgres, writing a resource row and its
  outbox row together is the default: one `BEGIN`/`COMMIT`, no special
  setup. The relay side can go further than polling, too — a trigger
  that `NOTIFY`s on insert, plus a relay that `LISTEN`s
  (`pgx.Conn.Exec(ctx, "LISTEN outbox_insert")` +
  `conn.WaitForNotification(ctx)`), delivers new events with no polling
  latency, and `SELECT ... FOR UPDATE SKIP LOCKED` lets several relay
  workers dequeue concurrently without fighting over the same rows —
  plain SQL, nothing pgx-specific to add. The one real wrinkle: `LISTEN`/
  `NOTIFY` needs a single dedicated connection held open for the relay's
  lifetime, not `pgxpool.Pool`'s normal acquire-per-call pattern — the
  listening connection can't be one the pool hands to someone else
  mid-wait. Getting this for real means giving `internal/outbox` a
  Postgres-shaped counterpart (it's hardcoded to `*mongo.Collection`
  today) — a refactor that depends on point 2 landing first, not
  something to start before there's a real Postgres-backed resource to
  emit events from.
- **Bulk inserts via `COPY`, not batched `INSERT`s.** `InsertMany` today
  batches one `INSERT` per doc via `pgx.Batch` (see [`store.md`](store.md))
  — correct, but `pgx.CopyFrom` is substantially faster for large
  slices, using Postgres' binary copy protocol instead of N insert
  statements. Worth it once `InsertMany` is handling production-sized
  batches, not before.
- **Real query plans.** [Query performance today](store.md#query-performance-today)
  already contrasts Mongo's no-`explain()` story with `EXPLAIN ANALYZE`;
  nothing in the scaffold surfaces it yet. A `Store.Explain` that runs
  `EXPLAIN (FORMAT JSON) <query>` and returns the plan would be a small
  addition once there's a real query worth tuning.
- **Connection pool tuning.** `pgxpool.New` today takes the connection
  string's own defaults — no exposed `MaxConns`/`MinConns`/health-check
  interval, the way `mongodb.Connect` reads `mongo.*` config keys. A
  `postgres.Connect`/`postgres.URI` pair mirroring `mongodb.go`'s is the
  natural place for that, and for point 8's TLS/timeout settings — new
  code, not a refactor of `Store` itself.

One thing *not* on this list: pgx doesn't do read/write splitting or
connection routing across replicas — that's a deployment concern
(PgBouncer/PgCat, or several `pgxpool.Pool`s keyed by role), not
something the driver or this `Store` should take on.

## What exists today

- `internal/platform/postgres.Store`: the method set above, backed by
  `pgxpool.Pool`, with an equality-only filter (`map[string]any` read as
  `column: value`, no operators) — real but intentionally minimal; not
  the resting place for point 1's filter design, just enough to prove
  the shape compiles and has somewhere to grow. A `nil` value means
  `IS NULL` (not SQL's always-false `= NULL`), and `InsertMany` rejects
  any doc whose keys don't exactly match `docs[0]`'s, rather than
  silently writing `NULL`s or dropping a column.
- `internal/platform/postgres.IsDuplicateKeyError`/`IsTimeout`: the two
  checks from point 3's table, mirroring `mongo-driver`'s helpers of the
  same name.
- A `postgres` service in `docker-compose.yml`, alongside `mongo`,
  `redis` and `pubsub`, so the scaffold's tests run against a real
  database, not a mock.
- No change to `internal/rest` or any existing resource — this is an
  isolated, new package; nothing above carries any regression risk for
  what's in production today.

Used standalone, the way point 2's recommended path does (no `rest` in
sight, same as `internal/outbox` talking to `*mongo.Collection`
directly):

```go
pool, err := pgxpool.New(ctx, os.Getenv("POSTGRES_CONNECTION_STRING"))
store := postgres.NewStore(pool, "tags")

docs, err := store.Find(ctx, postgres.Filter{"status": "draft"}, 0, 10,
	[]postgres.SortField{{Column: "created_at", Desc: true}}, nil)
```

Compare with the Mongo snippet in [`store.md`](store.md) — same shape
(`NewStore`, `Find` with a filter/skip/limit/sort/columns), different
filter type and no `rest.Resource` wrapping it yet.
