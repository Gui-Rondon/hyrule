[Português](README.pt-br.md)

# blog-apis

The hyrule framework's practices applied to a generic domain (a blog),
instead of theory: 4 small services, each demonstrating a different set
of capabilities.

| Service | Demonstrates |
|---|---|
| [`authors-api`](services/authors-api) | 100% generic CRUD: just a schema + `env.API.Register`, zero business Go |
| [`tags-api`](services/tags-api) | generic CRUD with a `Unique` field and a slug lookup — the same schema, with nothing rewritten, is [`cmd/hyrule-serve/example.yaml`](../../cmd/hyrule-serve/example.yaml) |
| [`categories-api`](services/categories-api) | a hierarchical resource: a materialized path recomputed on every write, a `GET /categories/tree` endpoint built straight from the `Store` interface (no `*mongo.Collection` anywhere) |
| [`posts-api`](services/posts-api) | the most complete example: relations (`author_id`, `category_id`, `tags`), a transactional outbox, custom `/publish` and `/schedule` endpoints (the latter writes to a Redis hash and to the outbox in the same transaction), optional Elasticsearch indexing |

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

## How this fits into the framework

This example isn't the framework — it's just a *use* of it. hyrule's own
documentation (the `Store` interface, the conditional outbox, resources
via YAML) lives at **https://gui-rondon.github.io/hyrule/**.
