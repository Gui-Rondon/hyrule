[Português](README.pt-br.md)

# hyrule

[![Go Reference](https://pkg.go.dev/badge/github.com/Gui-Rondon/hyrule.svg)](https://pkg.go.dev/github.com/Gui-Rondon/hyrule)
[![codecov](https://codecov.io/gh/Gui-Rondon/hyrule/branch/main/graph/badge.svg)](https://codecov.io/gh/Gui-Rondon/hyrule)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

A Go framework for scaffolding microservices fast, and testing their
flows just as fast: describe a resource (schema, methods, lookups) —
in a YAML file, no Go at all, or in Go when it needs its own logic —
and get a running REST API with:

- **Full CRUD, validation and pagination** from a schema, no
  hand-written handler.
- **ETags/`If-Match` built in** — optimistic concurrency, not
  something you wire up yourself.
- **Mongo or Postgres, same HTTP contract** — the YAML path stays
  Mongo-only for now; a hand-written resource picks either backend
  through `rest.Adapter` (`restpostgres` for Postgres, see the Adapter
  reference below) for CRUD, validation, ETags and querying.
- **A transactional outbox** (a write and the event it causes commit
  together, in the same transaction, or neither does), **`testkit`**
  (isolated database + HTTP client per test) and **dry run** (a whole
  service against real traffic, every write rolled back). Transactional outbox
  and dry run support both backends. YAML and the
  testkit database provisioner remain Mongo-specific; HTTP/token helpers work
  with Postgres through app.Build as well.

Under the hood it keeps the HTTP contract of Eve (Python) (`rest`),
which is where the project migrated from.

📖 **Full documentation: https://gui-rondon.github.io/hyrule/** — a
guided [Learn](https://gui-rondon.github.io/hyrule/docs/learn/intro)
path plus a complete
[Reference](https://gui-rondon.github.io/hyrule/docs/reference/architecture).

## Automatic Swagger documentation

Swagger is enabled by default for every Go/YAML resource: open `/api/docs`
(or `/swagger`), with OpenAPI 3.1 at `/api/openapi.json`. Set
`DisableSwagger: true` in Go or `disableSwagger: true` in YAML to hide a
resource from documentation while keeping its routes active. Docs are public;
protected operations require a token. See the [Swagger reference](website/docs/reference/swagger.md).

## Mongo and Postgres integration

Both backends use `app.Main`/`app.Build`, the same HTTP resource contract and
transactional event hooks. Start with the [Mongo and Postgres integration guide](website/docs/reference/backends.md):
complete Go services, connection settings, schema/index setup, tests and opt-ins.
The [integration review](website/docs/reference/integration-review.md) records
compatibility changes and gate evidence against `main`. Use a commit/release
containing this integration; changing a backend does not migrate data.

| | Mongo | Postgres |
|---|---|---|
| Resource | `Store: env.Store("items")` | `Adapter: restpostgres.NewAdapter(...)` |
| Bootstrap | `app.BackendMongo` (default) | `app.BackendPostgres` |
| Outbox | `outbox` | `outboxpostgres` |
| Running example | [posts-api](examples/blog-apis/services/posts-api) | [comments-api](examples/blog-apis/services/comments-api) |

## Mongo quickstart: a resource with no Go

```yaml
# resources.yaml
resources:
  - name: tags
    collection: tags
    additional_lookup: {field: slug, pattern: '^[\w-]+$'}
    resource_methods: [get, post]
    item_methods: [get, patch, put]
    schema:
      name: {type: string, required: true}
      slug: {type: string, required: true, unique: true}
      enabled: {type: boolean, default: true}
```

```bash
make env-up    # Mongo (27018), Postgres (5433), Pub/Sub (8085), Redis (6380)
echo '{"auth": {"enable": "false"}}' > config.json
RESOURCES_FILE=resources.yaml CONFIG_FILE=config.json \
  MONGO_CONNECTION_STRING='mongodb://localhost:27018/demo?directConnection=true' \
  PORT=8080 go run ./cmd/hyrule-serve
```

```bash
curl -X POST localhost:8080/tags -d '{"name":"Go","slug":"go"}'
curl localhost:8080/tags/go
```

That's `cmd/hyrule-serve` + `declarative` — it covers the
common case. A resource with its own logic (hooks, custom endpoints,
conditional validation) stays a small Go binary with `app.Main`, like the
5 examples in [`examples/blog-apis/`](examples/blog-apis/) — both paths
register resources the same way and coexist in the same process. The
[Learn guide](https://gui-rondon.github.io/hyrule/docs/learn/intro)
walks through building both kinds, step by step.

[`examples/standalone-tags-api/`](examples/standalone-tags-api/) is the
same quickstart as above, but as its own Go module (own `go.mod`) that
depends on this one — the proof that an external repository can really
`go get` this framework, not just import it from inside this monorepo.

## Postgres quickstart: Go resource with transactional events

The comments service uses `app.BackendPostgres`, UUID/version CAS, JSONB,
explicit migrations and a transactional outbox. From this checkout:

```sh
make env-up
curl -sf -X PUT http://localhost:8085/v1/projects/local-dev/topics/comments-to-publish
POSTGRES_CONNECTION_STRING='postgres://postgres:postgres@localhost:5433/postgres?sslmode=disable' \
  make comments-migrate MIGRATE_ARGS='-action prepare'
make build SERVICE=comments-api
printf '%s\n' '{"auth":{"enable":false},"pubsub":{"project_id":"local-dev","to_publish":"comments-to-publish"}}' > .local/comments-api.json
POSTGRES_CONNECTION_STRING='postgres://postgres:postgres@localhost:5433/postgres?sslmode=disable' \
  PUBSUB_EMULATOR_HOST=localhost:8085 CONFIG_FILE=.local/comments-api.json \
  PORT=8082 .local/comments-api
```

```sh
curl -X POST localhost:8082/comments -H 'Content-Type: application/json' \
  -d '{"post_id":"post-1","author_name":"Link","body":"Hello","status":"published"}'
curl -G localhost:8082/comments --data-urlencode 'where={"post_id":"post-1"}'
```

See [the example runbook](examples/blog-apis/README.md#comments-api-postgres--pubsub)
for Pub/Sub delivery and schema rollout. [The backend guide](website/docs/reference/backends.md)
shows a minimal Postgres service without the example's Pub/Sub dependency.

## Platform: scaffolding a multi-service project

```bash
go install github.com/Gui-Rondon/hyrule/cmd/hyrule@main

hyrule new my-platform
cd my-platform
go get github.com/Gui-Rondon/hyrule@main
hyrule service create articles

make env-up
make build SERVICE=articles
make run SERVICE=articles
```

`hyrule new`/`hyrule service create` are the only two commands today
(ADR-0004) — scaffolding, not a build/deploy orchestrator. The generated
project has its own `go.mod`, `Makefile`, and `docker-compose.yml`, in
the same shape as this repo's own; `dev`/`build --affected`/`deploy`
subcommands are future work (see the
[roadmap](https://gui-rondon.github.io/hyrule/docs/reference/roadmap)).
The [Platform guide](https://gui-rondon.github.io/hyrule/docs/platform/intro)
walks through this step by step, including copying
`examples/blog-apis`'s affected-build CI and deploy pattern into a
generated project.

## Documentation

The full docs are at **https://gui-rondon.github.io/hyrule/**:

| | |
|---|---|
| [Learn](https://gui-rondon.github.io/hyrule/docs/learn/intro) | a guided, step-by-step path through the framework, building up `examples/blog-apis` one concept at a time |
| [Platform](https://gui-rondon.github.io/hyrule/docs/platform/intro) | `hyrule new`/`hyrule service create`, and copying the affected-build CI and deploy pattern into a generated project |
| [Reference → Store](https://gui-rondon.github.io/hyrule/docs/reference/store) | the `Store` interface: what it decouples (and what it doesn't yet) |
| [Reference → Outbox](https://gui-rondon.github.io/hyrule/docs/reference/outbox) | the transactional outbox, and the conditional primitive that fires an event straight from the write |
| [Reference → resources.yaml](https://gui-rondon.github.io/hyrule/docs/reference/resources-yaml) | full reference of the resource YAML |
| [Reference → Dry run](https://gui-rondon.github.io/hyrule/docs/reference/dry-run) | running in dry run: everything runs, the transaction always rolls back |
| [Reference → Testkit](https://gui-rondon.github.io/hyrule/docs/reference/testkit) | testing a resource (isolated Mongo, HTTP client, tokens) |
| [Reference → Architecture](https://gui-rondon.github.io/hyrule/docs/reference/architecture) | the `services → behaviors → rest → platform` layering, and why it exists |
| [Reference → Querying](https://gui-rondon.github.io/hyrule/docs/reference/querying) | the `where=`/`sort`/`projection` query language and how it's parsed |
| [Reference → Adapter](https://gui-rondon.github.io/hyrule/docs/reference/adapter) | `rest.Adapter`, the seam a Postgres-backed resource (`restpostgres`) registers instead of `Store` |
| [Reference → Postgres backend (historical)](https://gui-rondon.github.io/hyrule/docs/reference/postgres-rfc) | the original design record for the Postgres backend, superseded by the Adapter reference above |

## Layout

    app/                           shared bootstrap (app.Main / app.Build)
    behaviors/                     reusable hooks: searchsync (ES), audit, events (outbox on write)
    rest/                          the HTTP contract: Service (rules, no HTTP) + API (HTTP) + Adapter (storage seam, Store for Mongo)
    restpostgres/                  rest.Adapter over platform/postgres — the Postgres-backed resource path
    outbox/                        transactional outbox, the Pub/Sub relay and the conditional primitive
    declarative/                   loads a Resource/Schema from YAML (cmd/hyrule-serve), Mongo only today
    platform/                      config, mongodb, postgres, auth, search, health, server, pubsub, redis
    testkit/                       per-test isolated Mongo, ES recorder, tokens
    internal/domain/               the one package that stays internal (slug generation)
    cmd/hyrule-serve/              generic binary: registers the resources of a YAML, no domain Go
    cmd/hyrule/                    the Platform CLI: hyrule new, hyrule service create
    ci/                            ci/affected (picks the services a diff affects)
    examples/blog-apis/            a real use of the framework (see that directory's README)
    examples/standalone-tags-api/  an external Go module (own go.mod) depending on this one
    website/                       source of https://gui-rondon.github.io/hyrule/ (Docusaurus)

## Development

Requires Go 1.27 and Docker.

    make env-up               # generic infra: Mongo (27018), Postgres (5433), Pub/Sub (8085), Redis (6380)
    make test                 # every test (uses real Mongo/Postgres from env-up)
    make unit                 # only the ones that need no external service
    make cover                # framework coverage, fails below 95%
    make cover-all            # historical aggregate (framework + examples/tools), minimum 85%
    make build SERVICE=tags-api
    make run SERVICE=tags-api  # :8081, auth disabled

To run the `blog-apis` example (which also needs Elasticsearch): see
[`examples/blog-apis/README.md`](examples/blog-apis/README.md).

Coverage uses all normal package tests (`./...`) with real MongoDB and PostgreSQL.
`make cover` measures the runtime framework (`app`, `behaviors`, `declarative`,
`outbox`, `outboxpostgres`, `platform`, `rest`, `restpostgres`); example services,
CLI/manual HA tools and `testkit` do not enter its denominator. Tests for those
packages still execute. `make cover-all` writes the historical aggregate scope
separately to `.local/coverage-all.out`; the framework profile remains
`.local/coverage.out`. Run these commands sequentially, after `make env-up`.

## License

[MIT](LICENSE) — © 2026 Guilherme Rondon.
