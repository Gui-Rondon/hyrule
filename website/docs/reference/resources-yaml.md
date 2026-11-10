---
sidebar_position: 3
---

# Resource via YAML

`internal/declarative` loads one or more resources from a YAML file;
`cmd/hyrule-serve` registers them with no domain-specific Go code at all.
It covers the common case — a resource with no custom hooks or endpoints
(`examples/blog-apis/services/authors-api` and `tags-api` are exactly
that shape today, hand-written in Go). When a resource needs its own
logic, it stays a small Go binary with `app.Main`, like
`examples/blog-apis/services/posts-api`.

```bash
RESOURCES_FILE=resources.yaml PORT=8080 go run ./cmd/hyrule-serve
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

## What has no YAML equivalent

Two fields of `evecompat.Field`/`Resource` carry arbitrary Go code, not
data — so they can't be expressed here:

- **`Field.Check`**: custom validation of a field (e.g. checking against
  another API).
- **`Resource.Validate`**: validation of the whole document after the
  schema.

Outside the schema, `Resource.Datasource` (`bson.M` filters/projection)
and every `Hooks` kind (`OnInsert`, `OnUpdate`, outbox, search,
audit...) are also out of the loader's scope — a resource that needs any
of those stays in Go. A resource built from YAML is a plain
`*evecompat.Resource`, so nothing stops you from attaching hooks to it in
Go before registering — `internal/declarative/declarative_test.go`'s
`TestBuiltResourceComposesWithConditionalOutbox` does exactly that.
