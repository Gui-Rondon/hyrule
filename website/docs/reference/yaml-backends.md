---
sidebar_position: 5
---

# Run YAML resources with Mongo or Postgres

Run these commands from the repository root, with Go and Docker Compose
installed. Choose one backend and run one server at a time on port 8080.
The two examples expose the same `/tags` routes.

## Describe the resource

Save this as `resources-mongo.yaml`:

```yaml
resources:
  - name: tags
    collection: tags
    backend: mongo
    resource_methods: [get, post]
    item_methods: [get, patch]
    schema:
      name: {type: string, required: true}
      slug: {type: string, required: true, unique: true}
```

For Postgres, save a copy as `resources-postgres.yaml` and change only
`backend: mongo` to `backend: postgres`. `collection` names the SQL table
in that case. Each resource's `backend` must match the process's `BACKEND`.
Omitting the YAML field or the environment variable defaults that setting
to Mongo independently; set both explicitly when using Postgres.

## Mongo {/* #mongo */}

Start the repository's replica set and wait for readiness:

```bash
docker compose up -d --wait mongo
```

Save `config-mongo.json`:

```json
{
  "auth": {"enable": false},
  "mongo": {
    "uri": "mongodb://localhost:27018/hyrule?directConnection=true",
    "query_timeout": "5s",
    "indexes": "tags slug:1 name=tags_slug_unique unique\n"
  }
}
```

This disables authentication for local experimentation. Mongo creates the
collection on first write; the index declaration enforces slug uniqueness
at the database level, including concurrent writes.

```bash
BACKEND=mongo CONFIG_FILE=config-mongo.json \
RESOURCES_FILE=resources-mongo.yaml PORT=8080 \
go run ./cmd/hyrule-serve
```

## Postgres {/* #postgres */}

Start Postgres and apply the resource table DDL before starting the API.
For this local example, use the existing `postgres` database:

```bash
docker compose up -d --wait postgres
docker compose exec -T postgres psql -v ON_ERROR_STOP=1 -U postgres -d postgres <<'SQL'
CREATE TABLE tags (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    doc jsonb NOT NULL DEFAULT '{}',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    version bigint NOT NULL DEFAULT 1,
    deleted boolean NOT NULL DEFAULT false
);
CREATE UNIQUE INDEX tags_slug_unique ON tags ((doc->>'slug'));
SQL
```

Run this migration once on a fresh database. `hyrule-serve` does not create
Postgres resource tables or migrate existing schemas. `unique: true` enables
validation; the unique index also protects concurrent writes.

Save `config-postgres.json`:

```json
{
  "auth": {"enable": false},
  "postgres": {
    "uri": "postgres://postgres:postgres@localhost:5433/postgres?sslmode=disable",
    "max_conns": 10,
    "min_conns": 1,
    "connect_timeout": "5s",
    "query_timeout": "5s",
    "statement_timeout": "5s",
    "lock_timeout": "1s"
  }
}
```

These are example local settings: `max_conns`/`min_conns` size the pool,
`connect_timeout` bounds connection establishment, `query_timeout` bounds
resource operations, and `statement_timeout`/`lock_timeout` apply on Postgres
connections. Authentication is disabled and TLS is disabled in this local URI;
configure both for your deployment. See [backend configuration](backends.md).

```bash
BACKEND=postgres CONFIG_FILE=config-postgres.json \
RESOURCES_FILE=resources-postgres.yaml PORT=8080 \
go run ./cmd/hyrule-serve
```

`MONGO_CONNECTION_STRING` and `POSTGRES_CONNECTION_STRING` override the
respective configured URI when present. Check them if the server connects
to an unexpected database.

## Exercise either API

```bash
curl -i -X POST localhost:8080/tags \
  -H 'Content-Type: application/json' -d '{"name":"Go","slug":"go"}'
curl -G localhost:8080/tags --data-urlencode 'where={"slug":"go"}'
```

Use the actual `_id` and `_etag` returned by POST for item operations:

```bash
curl -i localhost:8080/tags/ID
curl -i -X PATCH localhost:8080/tags/ID \
  -H 'Content-Type: application/json' -H 'If-Match: "ETAG"' \
  -d '{"name":"Golang"}'
```

IDs and ETags are opaque and differ between backends. Postgres uses UUIDs
and RFC 7396 merge patch (`null` removes a field); Mongo uses ObjectIDs and
Eve merge semantics. Changing the backend does not migrate data.
See the [YAML field reference](resources-yaml.md) for more resource options
and [adapter contracts](adapter.md) for semantic differences.
