---
sidebar_position: 0
---

# Integrating Mongo and Postgres

Both backends use `app.Main`/`app.Build`, `rest.Resource`, HTTP validation,
ETags, hooks, authentication, health checks, metrics and rollback-based dry run.
Choose a backend explicitly in Go; Mongo remains the zero-value default for
existing services. Equal support does not mean identical storage semantics.

| Integration point | Mongo | Postgres |
|---|---|---|
| Bootstrap | `app.BackendMongo` (zero value) | `app.BackendPostgres` |
| Persistence | `Resource.Store = env.Store(collection)` | `Resource.Adapter = restpostgres.NewAdapter(...)` |
| Connection in Setup | `env.DB` | `env.Postgres`, `env.PostgresTx` |
| Resource identity | BSON ObjectID | UUID |
| Index declaration | `Spec.Indexes` / `mongo.indexes` | `Spec.PostgresIndexes` |
| Transactional events | `outbox.New(env.DB)` | `outboxpostgres.Outbox{Pool: env.Postgres}` |
| Dry run | Replica set, rollback and WriteGuard | Same-pool transaction rollback |
| Tests | `testkit.Service` / real Mongo | `app.Build` + isolated real Postgres + `testkit.NewClient` |
| YAML / generated CLI resource | Supported | Supported via `backend: postgres` in `resources.yaml` — see [Resource via YAML](resources-yaml.md) |

## Start local dependencies

From a checkout of this integration:

```sh
make env-up
```

The standard compose starts Mongo (27018), Postgres (5433), Pub/Sub (8085) and
Redis (6380). It is separate from the manual HA lab, which this workspace uses
on **5540**. Do not point HA commands at 5434 here; another project owns it.
For an external module, install a release or commit containing this integration;
`main` is not assumed to contain it yet. In that module, save **one** of the
following complete programs as `main.go` and run `go mod tidy`.

## Mongo: register and run a service

```go
package main

import (
    "context"
    "net/http"

    "github.com/Gui-Rondon/hyrule/app"
    "github.com/Gui-Rondon/hyrule/rest"
)

func main() {
    app.Main(app.Spec{
        Name: "items-api", Backend: app.BackendMongo, DefaultDB: "items_demo",
        Indexes: []byte("items name:1 name=items_name_idx\n"),
        Setup: func(ctx context.Context, env *app.Env) error {
            env.API.Register(&rest.Resource{
                Name: "items", Store: env.Store("items"),
                Schema: rest.Schema{"name": {Type: rest.TypeString, Required: true}},
                ResourceMethods: []string{http.MethodGet, http.MethodPost},
                ItemMethods: []string{http.MethodGet, http.MethodPatch, http.MethodPut, http.MethodDelete},
            })
            return nil
        },
    })
}
```

Mongo creates the collection on first write. For transactional writes, outbox
and dry run, connect to a replica set; the local compose provides one. A
standalone Mongo uses a pass-through transactor and does not provide atomic
multi-write/outbox guarantees. The legacy `Resource.Store` registration remains
supported: an internal Mongo adapter is created automatically.

```sh
printf '%s\n' '{"auth":{"enable":false}}' > config.json
CONFIG_FILE=config.json PORT=8080 \
MONGO_CONNECTION_STRING='mongodb://localhost:27018/items_demo?directConnection=true' \
  go run .
```

Declare indexes at startup, independently of friendly schema validation:

```go
spec.Indexes = []byte("items name:1 name=items_name_idx\n")
// Add `unique` when the business contract requires it; Schema.Unique alone
// is a pre-check, not protection against concurrent duplicate writes.
```

`mongo.indexes_mode` is `create` (default), `check` or `off`; dry run downgrades
create to check. Embedded `.indices.conf` declarations and `mongo.indexes`
overrides are merged by the bootstrap. See [Store and indexes](store.md).
For the YAML alternative, follow [resources.yaml](resources-yaml.md).

## Postgres: migrate, register and run a service

Create the database once, then apply the table DDL as an explicit migration:

```sh
docker compose exec -T postgres psql -U postgres -d postgres -c 'CREATE DATABASE items_demo'
docker compose exec -T postgres psql -U postgres -d items_demo <<'SQL'
CREATE TABLE items (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    doc jsonb NOT NULL DEFAULT '{}',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    version bigint NOT NULL DEFAULT 1,
    deleted boolean NOT NULL DEFAULT false
);
SQL
```

```go
package main

import (
    "context"
    "net/http"

    "github.com/Gui-Rondon/hyrule/app"
    "github.com/Gui-Rondon/hyrule/platform/postgres"
    "github.com/Gui-Rondon/hyrule/rest"
    "github.com/Gui-Rondon/hyrule/restpostgres"
)

func main() {
    schema := rest.Schema{"name": {Type: rest.TypeString, Required: true}}
    app.Main(app.Spec{
        Name: "items-api", Backend: app.BackendPostgres, DefaultDB: "items_demo",
        PostgresIndexes: []postgres.IndexSpec{{
            Table: "items", Fields: []postgres.IndexField{{Field: "name"}},
        }},
        Setup: func(ctx context.Context, env *app.Env) error {
            adapter, err := restpostgres.NewAdapter(
                postgres.NewStore(env.Postgres, "items"), env.PostgresTx,
                restpostgres.Options{Schema: schema, Retry: rest.DefaultRetryOptions()},
            )
            if err != nil { return err }
            env.API.Register(&rest.Resource{
                Name: "items", Adapter: adapter, Schema: schema,
                ResourceMethods: []string{http.MethodGet, http.MethodPost},
                ItemMethods: []string{http.MethodGet, http.MethodPatch, http.MethodPut, http.MethodDelete},
            })
            return nil
        },
    })
}
```

Reuse `env.PostgresTx`: it carries the request's pool and dry-run policy.
`platform/postgres.Store` is a flat-column helper, **not** an implementation of
`rest.Store`; JSONB resources need `restpostgres.NewAdapter`. The bootstrap
installs shared functions before Setup and ensures declared indexes after it.
It does not create your resource table or run schema migrations. The runnable
comments example uses an explicit staged migration runner and startup schema
checks; see its [migration runbook](https://github.com/Gui-Rondon/hyrule/blob/main/examples/blog-apis/internal/commentsschema/README.md).

```sh
printf '%s\n' '{"auth":{"enable":false}}' > config.json
CONFIG_FILE=config.json PORT=8080 \
POSTGRES_CONNECTION_STRING='postgres://postgres:postgres@localhost:5433/items_demo?sslmode=disable' \
  go run .
```

`postgres.indexes_mode` is also create/check/off; dry run downgrades create to
check. Indexes are Go `IndexSpec` values, not Mongo `.indices.conf` syntax.
`EnsureIndexes` detects names, not definition equivalence: use a new explicit
name when adopting a different expression; do not assume startup migrates it.

## Exercise either service

The same requests work with either program; keep IDs and ETags opaque and use
the actual response values for subsequent requests:

```sh
curl -i -X POST localhost:8080/items -H 'Content-Type: application/json' -d '{"name":"first"}'
curl -G localhost:8080/items --data-urlencode 'where={"name":"first"}' --data-urlencode 'sort=name'
curl -i localhost:8080/items/ID
curl -i -X PATCH localhost:8080/items/ID -H 'Content-Type: application/json' \
  -H 'If-Match: "ETAG"' -d '{"name":"second"}'
```

Stale ETags return 412. Do not convert an ObjectID into a UUID or reuse an ETag
from a different database. Postgres PATCH uses RFC 7396 (`null` removes a key);
Mongo retains Eve deep-merge/null semantics. Both reject invalid required data,
but equal HTTP routes do not imply byte-identical document or error behavior.
See [Adapter contracts](adapter.md) and [querying](querying.md).

## Configuration, lifecycle and integration boundaries

| Concern | Mongo | Postgres |
|---|---|---|
| Connection precedence | `MONGO_CONNECTION_STRING`, `mongo.uri`, individual `mongo.*` fields | `POSTGRES_CONNECTION_STRING`, `postgres.uri`, individual `postgres.*` fields |
| Per-read application budget | `mongo.query_timeout` (10s default) | `postgres.query_timeout` (10s default) |
| Connection/pool settings | Mongo URI driver options | `postgres.max_conns`, `min_conns`, `max_conn_lifetime`, `max_conn_idle_time`, `health_check_period`, `connect_timeout` |
| Server statement/lock limits | Mongo driver/server settings | `postgres.statement_timeout`, `postgres.lock_timeout` (Go durations, sent in milliseconds) |
| Business writes | Reuse context and Mongo session | Use `Store.Querier(ctx)` with the same pool/context |
| Background tasks | `env.Go` | `env.Go` |

Always include the intended database explicitly in a connection URI. In
particular, a Postgres URI without a database follows pgx resolution rather
than applying `Spec.DefaultDB`; individual host/port config uses DefaultDB.
Mongo also uses DefaultDB when its URI omits the database. Do not infer that
backend-specific driver options are interchangeable. Unknown Spec.Backend
values are rejected before configuration/connection setup.

`app.Run` owns connections it opens; `app.Build` receives dependencies whose
lifetime belongs to the caller. Build does not start background tasks without
`Deps.Start`. `env.Go` is suppressed in dry run. Shutdown cancels the serving
context; workers must respect cancellation. External effects belong in an
outbox/after-commit consumer or a route explicitly refused in dry run, not in
transaction callbacks. Authentication remains JWT validation, not user-session
management.

## Events, dry run and tests

- [Transactional outbox](outbox.md): equivalent registration for both backends,
  at-least-once delivery, relay differences and Postgres dead-event replay.
- [Dry run](dry-run.md): transactions, setup caveats and external side effects.
- [Testing](testkit.md): real database isolation and app.Build examples for both.
- [Integration review and gate evidence](integration-review.md): main comparison,
  compatibility changes, actual measurements and limitations.

## Adopt Postgres opt-ins deliberately

| Opt-in | Where to set it | Default preserved | Adoption and reversal |
|---|---|---|---|
| Typed sort index | `IndexField.Mode = postgres.IndexFieldTypedSort`, explicit new name | JSONB legacy index expression | Audit castable data and write costs; String/Integer/Number/Boolean only, no typed Unique/Sparse/datetime. Remove declaration and explicitly migrate/drop an unused new index to reverse. |
| Safe datetime sort | `restpostgres.Options.SafeDateTimeSort = true` | Direct cast and errors for invalid data | Audit formats; rejected values sort as SQL NULL, original documents unchanged. Existing legacy datetime helper index can match. Remove option to restore direct cast. |
| Custom plan policy | Winning URI: `plan_cache_mode=force_custom_plan` | Existing server/role policy (normally auto) | Applies to the whole pool, including writes/outbox; adds per-execution planning cost. Measure real workload. Remove URI parameter and recreate pool to reverse. |

Explicit sparse presence (`{"slug":{"$eq":"value","$exists":true}}`) can make a
legacy partial index usable in custom plans. No implicit presence guard is
added: an earlier approach regressed UUID plans. Generic field parameters and
deep offsets remain observational; an index declaration is not a guarantee of
planner use or production latency. Nested datetime and typed Unique/Sparse
remain separate extensions. `resources.yaml`'s `backend:` field (see
[Resource via YAML](resources-yaml.md)) picks which backend a declarative
resource uses; no backend conversion or data migration is performed by
changing it or Spec.Backend.
