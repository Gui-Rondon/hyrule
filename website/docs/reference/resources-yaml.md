---
sidebar_position: 4
---

# Resource via YAML

`declarative` loads one or more resources from a YAML file;
`cmd/hyrule-serve` registers them with no domain-specific Go code at all.
It covers the common case — a resource with no custom hooks or endpoints
(`examples/blog-apis/services/authors-api` and `tags-api` are exactly
that shape today, hand-written in Go). When a resource needs its own
logic, it stays a small Go binary with `app.Main`, like
`examples/blog-apis/services/posts-api`.

```bash
RESOURCES_FILE=resources.yaml PORT=8080 go run ./cmd/hyrule-serve
```

## A complete example

This `resources.yaml` is the YAML equivalent of
`examples/blog-apis/services/tags-api` (hand-written in Go today) —
every request/response below is real output from running it, not
invented for these docs:

```yaml
resources:
  - name: tags
    collection: tags
    item_title: tag
    schema:
      name: {type: string, required: true}
      slug: {type: string, required: true, unique: true}
      enabled: {type: boolean, default: true}
    additional_lookup:
      field: slug
      pattern: '^[\w-]+$'
    resource_methods: [get, post]
    item_methods: [get, patch, put]
    public_methods: [get]
    public_item_methods: [get]
```

Auth defaults to on (`app.Env`'s `auth.enable` config), which would
401/403 the `POST`s below since they're not in `public_methods` — the
run below disables it, the same `CONFIG_FILE` trick every other
example in this repo uses:

```bash
echo '{"auth": {"enable": false}}' > config.json
RESOURCES_FILE=resources.yaml CONFIG_FILE=config.json PORT=8080 go run ./cmd/hyrule-serve
```

Creating one — the response is `WriteResult`'s envelope (meta fields
only, Eve's `BANDWIDTH_SAVER`), not the document itself:

```bash
curl -X POST localhost:8080/tags -H 'Content-Type: application/json' \
  -d '{"name":"Go","slug":"go"}'
```

```json
{
  "_created": "Sat, 14 Nov 2026 21:38:49 GMT",
  "_etag": "2db9a568b830ed8c6e0f00e6e5e432e52f3bed03",
  "_id": "6af8d4e9e54b1d88e572d5e8",
  "_links": {"self": {"href": "tags/6af8d4e9e54b1d88e572d5e8", "title": "tag"}},
  "_status": "OK",
  "_updated": "Sat, 14 Nov 2026 21:38:49 GMT"
}
```

`additional_lookup` lets the same item route resolve by `slug`, not
just `_id` — and (`public_item_methods: [get]`) with no token:

```bash
curl localhost:8080/tags/go
```

```json
{
  "_created": "Sat, 14 Nov 2026 21:38:49 GMT",
  "_etag": "2db9a568b830ed8c6e0f00e6e5e432e52f3bed03",
  "_id": "6af8d4e9e54b1d88e572d5e8",
  "_links": {
    "collection": {"href": "tags", "title": "tags"},
    "parent": {"href": "/", "title": "home"},
    "self": {"href": "tags/6af8d4e9e54b1d88e572d5e8", "title": "tag"}
  },
  "_updated": "Sat, 14 Nov 2026 21:38:49 GMT",
  "enabled": true,
  "name": "Go",
  "slug": "go"
}
```

`unique: true` on `slug` turns a repeat into a validation issue, not a
raw duplicate-key error:

```bash
curl -X POST localhost:8080/tags -H 'Content-Type: application/json' \
  -d '{"name":"Golang","slug":"go"}'
```

```json
{
  "_error": {"code": 422, "message": "Insertion failure: 1 document(s) contain(s) error(s)"},
  "_issues": {"slug": "value 'go' is not unique"},
  "_status": "ERR"
}
```

Listing combines `where=` (see [`querying.md`](querying.md)) with
`pagination` — `_links` is always there, but `_meta` (`page`/
`max_results`/`total`) only appears when pagination isn't `disabled`.
One more tag first, this one disabled, so there's something for
`where=` to actually narrow down:

```bash
curl -X POST localhost:8080/tags -H 'Content-Type: application/json' \
  -d '{"name":"Rust","slug":"rust","enabled":false}'
curl -G localhost:8080/tags --data-urlencode 'where={"enabled":false}'
```

```json
{
  "_items": [
    {"_id": "6af8d4ede54b1d88e572d5ea", "name": "Rust", "slug": "rust", "enabled": false, "...": "..."}
  ],
  "_links": {"parent": {"href": "/", "title": "home"}, "self": {"href": "tags?where={\"enabled\":false}", "title": "tags"}},
  "_meta": {"max_results": 25, "page": 1, "total": 1}
}
```

## Reference

```yaml
resources:
  - name: tags            # required; becomes Resource.Name and Resource.URL
    collection: tags       # the Mongo collection (env.Store(collection))
    item_title: tag        # optional; defaults to the name without a trailing "s"

    schema:
      <field>:
        type: string | boolean | integer | number | dict | list | objectid | datetime
        required: true
        nullable: true
        unique: true
        default: <any YAML value>
        allowed: [a, b, c]
        min_length: 1
        max_length: 100
        min: 0            # number
        max: 100          # number
        not_empty: true
        allow_empty: true
        regex: '^[\w-]+$' # compiled; a bad regex fails the build
        allow_unknown: true  # dict: accepts keys outside schema
        schema: {...}        # dict: validates its keys (recursive)
        items: {...}         # list: validates every element (recursive)

    allow_unknown: true   # accepts fields outside schema in the document
    additional_lookup:
      field: slug          # GET /<resource>/<value> looks up by this field
      pattern: '^[\w-]+$'  # regex that tells a lookup apart from an _id

    resource_methods: [get, post]          # GET/POST on the collection
    item_methods: [get, patch, put]        # GET/PATCH/PUT on the item
    public_methods: [get]                  # no authentication required
    public_item_methods: [get]

    pagination:
      default: 25
      limit: 50
      optimize_for_speed: false  # true: no total count (_meta.total)
      disabled: false            # true: returns everything, no pagination

    soft_delete: true      # DELETE marks _deleted instead of removing
    cache_control: "max-age=20"
    cache_expires: 20
```

Method names accept either case (`get` or `GET`).

`resource_methods`/`item_methods` decide which HTTP methods exist at
all (an unlisted one is a `405`); `public_methods`/`public_item_methods`
only narrow that further, from "needs a token" to "doesn't" — a method
in `public_methods` but not `resource_methods` is still a `405`, never
a public hole into something unlisted. In the example above, `GET` is
public on both routes, `POST`/`PATCH`/`PUT` always need a token.

## What has no YAML equivalent

Two fields of `rest.Field`/`Resource` carry arbitrary Go code, not
data — so they can't be expressed here:

- **`Field.Check`**: custom validation of a field (e.g. checking against
  another API).
- **`Resource.Validate`**: validation of the whole document after the
  schema.

Outside the schema, `Resource.Datasource` (`bson.M` filters/projection)
and every `Hooks` kind (`OnInsert`, `OnUpdate`, outbox, search,
audit...) are also out of the loader's scope — a resource that needs any
of those stays in Go. A resource built from YAML is a plain
`*rest.Resource`, so nothing stops you from attaching hooks to it in
Go before registering — `declarative/declarative_test.go`'s
`TestBuiltResourceComposesWithConditionalOutbox` does exactly that.
