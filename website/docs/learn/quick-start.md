---
sidebar_position: 2
---

# Quick Start

This page gets a real API running against a real MongoDB, in about five
minutes, with no Go code written at all.

:::info[You will learn]
- How to start the local infrastructure (Mongo, Pub/Sub, Redis)
- How to write a minimal `resources.yaml`
- How to run it with `cmd/hyrule-serve` and talk to it with `curl`
:::

## 1. Start the infrastructure

hyrule needs a MongoDB replica set (so transactions work), plus Pub/Sub
and Redis for the outbox examples later in this guide. A
`docker-compose.yml` ships with the repo for exactly this:

```bash
git clone https://github.com/Gui-Rondon/hyrule.git
cd hyrule
make env-up
```

This starts Mongo on `localhost:27018`, a Pub/Sub emulator on `:8085`,
and Redis on `:6380` — nothing is exposed outside your machine.

## 2. Describe a resource

Create `resources.yaml`:

```yaml
resources:
  - name: tags
    collection: tags
    additional_lookup:
      field: slug
      pattern: '^[\w-]+$'
    resource_methods: [get, post]
    item_methods: [get, patch, put]
    schema:
      name:
        type: string
        required: true
      slug:
        type: string
        required: true
        unique: true
      enabled:
        type: boolean
        default: true
```

This declares a `tags` resource stored in the `tags` collection, with
two extra behaviors: `additional_lookup` lets you `GET` an item by its
`slug` instead of its `_id`, and `unique: true` on `slug` rejects a
duplicate before it ever reaches the database.

## 3. Run it

```bash
RESOURCES_FILE=resources.yaml CONFIG_FILE=/dev/null \
  MONGO_CONNECTION_STRING='mongodb://localhost:27018/demo?directConnection=true' \
  PORT=8080 go run ./cmd/hyrule-serve
```

`CONFIG_FILE=/dev/null` disables authentication for this quick test —
see [Reference → `resources.yaml`](/docs/reference/resources-yaml) for
what else goes in that file.

## 4. Talk to it

```bash
curl -X POST localhost:8080/tags -H 'Content-Type: application/json' \
  -d '{"name":"Go","slug":"go"}'
# {"_created":"...","_etag":"...","_id":"...","_status":"OK","_updated":"..."}

curl localhost:8080/tags/go
# {"name":"Go","slug":"go","enabled":true,"_id":"...", ...}

curl -X POST localhost:8080/tags -H 'Content-Type: application/json' \
  -d '{"name":"Golang","slug":"go"}'
# 422 Unprocessable Entity — slug "go" already exists
```

Three requests, zero lines of Go, a real MongoDB document, a real
validation rule enforced. That's `cmd/hyrule-serve` +
`internal/declarative` reading your YAML and building an
`evecompat.Resource` from it — the exact same type a hand-written Go
service builds, which is what the next page digs into.

:::tip[Recap]
- `make env-up` starts Mongo/Pub-Sub/Redis for local development.
- A resource is a YAML file; `cmd/hyrule-serve` turns it into a running API.
- `additional_lookup` and `unique` are two small examples of behavior you
  get from data, not code — [Your First Resource](./your-first-resource.md)
  covers the rest of what a schema can express.
:::
