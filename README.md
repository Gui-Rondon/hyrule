[Português](README.pt-br.md)

# hyrule

[![Go Reference](https://pkg.go.dev/badge/github.com/Gui-Rondon/hyrule.svg)](https://pkg.go.dev/github.com/Gui-Rondon/hyrule)
[![codecov](https://codecov.io/gh/Gui-Rondon/hyrule/branch/main/graph/badge.svg)](https://codecov.io/gh/Gui-Rondon/hyrule)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

A Go framework for scaffolding microservices fast, and testing their
flows just as fast: describe a resource (schema, methods, lookups) in a
YAML file — no Go at all — and get a running REST API with full CRUD,
validation, ETags, pagination and a transactional outbox, on Mongo
(Postgres support in progress). A resource with its own logic stays a
small Go binary. Either way, `testkit` gives each test an isolated
database and an HTTP client, and dry run lets a whole service run
against real traffic with every write rolled back — so you can exercise
a service's actual flows without standing up the real stack or faking
your way through it. Under the hood it keeps the HTTP contract of Eve
(Python) (`rest`), which is where the project migrated from.

📖 **Full documentation: https://gui-rondon.github.io/hyrule/** — a
guided [Learn](https://gui-rondon.github.io/hyrule/docs/learn/intro)
path plus a complete
[Reference](https://gui-rondon.github.io/hyrule/docs/reference/architecture).

## Quickstart: a resource with no Go

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
make env-up    # Mongo (27018), Pub/Sub (8085), Redis (6380)
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
4 examples in [`examples/blog-apis/`](examples/blog-apis/) — both paths
register resources the same way and coexist in the same process. The
[Learn guide](https://gui-rondon.github.io/hyrule/docs/learn/intro)
walks through building both kinds, step by step.

[`examples/standalone-tags-api/`](examples/standalone-tags-api/) is the
same quickstart as above, but as its own Go module (own `go.mod`) that
depends on this one — the proof that an external repository can really
`go get` this framework, not just import it from inside this monorepo.

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

## Documentation

The full docs are at **https://gui-rondon.github.io/hyrule/**:

| | |
|---|---|
| [Learn](https://gui-rondon.github.io/hyrule/docs/learn/intro) | a guided, step-by-step path through the framework, building up `examples/blog-apis` one concept at a time |
| [Reference → Store](https://gui-rondon.github.io/hyrule/docs/reference/store) | the `Store` interface: what it decouples (and what it doesn't yet) |
| [Reference → Outbox](https://gui-rondon.github.io/hyrule/docs/reference/outbox) | the transactional outbox, and the conditional primitive that fires an event straight from the write |
| [Reference → resources.yaml](https://gui-rondon.github.io/hyrule/docs/reference/resources-yaml) | full reference of the resource YAML |
| [Reference → Dry run](https://gui-rondon.github.io/hyrule/docs/reference/dry-run) | running in dry run: everything runs, the transaction always rolls back |
| [Reference → Testkit](https://gui-rondon.github.io/hyrule/docs/reference/testkit) | testing a resource (isolated Mongo, HTTP client, tokens) |
| [Reference → Architecture](https://gui-rondon.github.io/hyrule/docs/reference/architecture) | the `services → behaviors → rest → platform` layering, and why it exists |
| [Reference → Querying](https://gui-rondon.github.io/hyrule/docs/reference/querying) | the `where=`/`sort`/`projection` query language and how it's parsed |
| [Reference → Postgres backend](https://gui-rondon.github.io/hyrule/docs/reference/postgres-rfc) | the open design questions for a second, non-Mongo backend |

## Layout

    app/                           shared bootstrap (app.Main / app.Build)
    behaviors/                     reusable hooks: searchsync (ES), audit, events (outbox on write)
    rest/                          the HTTP contract: Service (rules, no HTTP) + API (HTTP) + Store (storage interface)
    outbox/                        transactional outbox, the Pub/Sub relay and the conditional primitive
    declarative/                   loads a Resource/Schema from YAML (cmd/hyrule-serve)
    platform/                      config, mongodb, auth, search, health, server, pubsub, redis
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

    make env-up               # generic infra: Mongo rs (27018), Pub/Sub (8085), Redis (6380)
    make test                 # every test (uses the Mongo from env-up)
    make unit                 # only the ones that need no external service
    make cover                # coverage, fails below 85%
    make build SERVICE=tags-api
    make run SERVICE=tags-api  # :8081, auth disabled

To run the `blog-apis` example (which also needs Elasticsearch): see
[`examples/blog-apis/README.md`](examples/blog-apis/README.md).

## License

[MIT](LICENSE) — © 2026 Guilherme Rondon.
