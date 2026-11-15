[Português](README.pt-br.md)

# blog-apis

The hyrule framework's practices applied to a generic domain (a blog),
instead of theory: 5 small services, each demonstrating a different set
of capabilities.

| Service | Demonstrates |
|---|---|
| [`authors-api`](services/authors-api) | 100% generic CRUD: just a schema + `env.API.Register`, zero business Go |
| [`tags-api`](services/tags-api) | generic CRUD with a `Unique` field and a slug lookup — the same schema, with nothing rewritten, is [`cmd/hyrule-serve/example.yaml`](../../cmd/hyrule-serve/example.yaml) |
| [`categories-api`](services/categories-api) | a hierarchical resource: a materialized path recomputed on every write, a `GET /categories/tree` endpoint built straight from the `Store` interface (no `*mongo.Collection` anywhere) |
| [`posts-api`](services/posts-api) | the most complete example: relations (`author_id`, `category_id`, `tags`), a transactional outbox, custom `/publish` and `/schedule` endpoints (the latter writes to a Redis hash and to the outbox in the same transaction), optional Elasticsearch indexing |
| [`comments-api`](services/comments-api) | Postgres via `app.Main`/`restpostgres`, a declarative index and a transactional outbox publishing `comment.published` to Pub/Sub |

## Running locally

```bash
# generic infra (mongo/pubsub/redis) + this example's Elasticsearch
docker compose -f examples/blog-apis/docker-compose.yml --profile debug up -d --wait

SERVICES_DIR=examples/blog-apis/services make build SERVICE=posts-api
echo '{"auth": {"enable": "false"}, "pubsub": {"project_id": "local-dev"}}' > config.json
MONGO_CONNECTION_STRING='mongodb://localhost:27018/posts?directConnection=true' \
  PUBSUB_EMULATOR_HOST=localhost:8085 \
  PORT=8081 CONFIG_FILE=config.json .local/posts-api
```

Without a `CONFIG_FILE` pointing at a file with
`{"auth": {"enable": false}}`, the endpoints that require a token
(`/publish`, `/schedule`) will ask for authentication — see
`testkit` to generate a test token, or turn off `auth.enable` in
the local config.

## CI: building only what changed

[`.github/workflows/blog-apis-affected.yml`](../../.github/workflows/blog-apis-affected.yml)
runs [`ci/affected`](../../ci) on every push/PR, builds only the
services it reports affected, and calls each one's
[`deploy/<service>/deploy.sh`](deploy) — a stub today, the seam where a
real provider's deploy command goes (see
[ADR-0006](https://gui-rondon.github.io/hyrule/docs/reference/architecture-decisions#adr-0006--fase-4-operational-scope-affected-build-ci-not-a-deploy-system)).
This workflow is itself part of the example — copy and adapt it, the
way a project scaffolded by `hyrule new` would.

## How this fits into the framework

This example isn't the framework — it's just a *use* of it. hyrule's own
documentation (the `Store` interface, the conditional outbox, resources
via YAML) lives at **https://gui-rondon.github.io/hyrule/**.

## comments-api: Postgres + Pub/Sub

```bash
make env-up
curl -sf -X PUT http://localhost:8085/v1/projects/local-dev/topics/comments-to-publish
make build SERVICE=comments-api
printf '%s\n' '{"auth":{"enable":false},"pubsub":{"project_id":"local-dev","to_publish":"comments-to-publish"}}' > .local/comments-api.json
POSTGRES_CONNECTION_STRING='postgres://postgres:postgres@localhost:5433/postgres?sslmode=disable' \
  PUBSUB_EMULATOR_HOST=localhost:8085 CONFIG_FILE=.local/comments-api.json \
  PORT=8082 .local/comments-api
```

A write that changes `status` to `published` (including creating an already
published comment) adds `comment.published` in the same Postgres transaction.
The event body is the comment document; its ordering key is the comment UUID.
`pubsub.to_publish` defaults to `comments-to-publish`; provision the topic before
running the relay. Delivery is at-least-once: consumers deduplicate by `event_id`.
A dry run rolls back both writes and starts no relay. `/healthcheck` checks
Postgres, the outbox backlog and the destination topic. The e2e harness verifies
actual delivery to an isolated Pub/Sub subscription as well as HTTP behavior.
