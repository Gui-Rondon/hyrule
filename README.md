[Português](README.pt-br.md)

# hyrule

A Go framework for building REST APIs on top of Mongo: describe a
resource (schema, methods, lookups) and get full CRUD with validation,
ETags, pagination and a transactional outbox — in Go, or with no Go at
all, via YAML. Under the hood it keeps the HTTP contract of Eve (Python)
(`internal/evecompat`), which is where the project migrated from.

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

That's `cmd/hyrule-serve` + `internal/declarative` — it covers the
common case. A resource with its own logic (hooks, custom endpoints,
conditional validation) stays a small Go binary with `app.Main`, like the
4 examples in [`examples/blog-apis/`](examples/blog-apis/) — both paths
register resources the same way and coexist in the same process. The
[Learn guide](https://gui-rondon.github.io/hyrule/docs/learn/intro)
walks through building both kinds, step by step.

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
| [Reference → Architecture](https://gui-rondon.github.io/hyrule/docs/reference/architecture) | the `services → behaviors → evecompat → platform` layering, and why it exists |

## Layout

    internal/app/              shared bootstrap (app.Main / app.Build)
    internal/behaviors/        reusable hooks: searchsync (ES), audit, events (outbox on write)
    internal/evecompat/        the HTTP contract: Service (rules, no HTTP) + API (HTTP) + Store (storage interface)
    internal/outbox/           transactional outbox, the Pub/Sub relay and the conditional primitive
    internal/declarative/      loads a Resource/Schema from YAML (cmd/hyrule-serve)
    internal/platform/         config, mongodb, auth, search, health, server, pubsub, redis
    internal/testkit/          per-test isolated Mongo, ES recorder, tokens
    cmd/hyrule-serve/          generic binary: registers the resources of a YAML, no domain Go
    ci/                        ci/affected (picks the services a diff affects)
    examples/blog-apis/        a real use of the framework (see that directory's README)
    website/                  source of https://gui-rondon.github.io/hyrule/ (Docusaurus)

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
