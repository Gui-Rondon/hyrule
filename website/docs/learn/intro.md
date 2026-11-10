---
sidebar_position: 1
---

# Introduction

hyrule is a Go framework for scaffolding microservices fast, and
testing their flows just as fast. You describe a **resource** — its
fields, its validation rules, which HTTP methods it answers to — and
hyrule gives you a working API: CRUD, request validation, ETags for
optimistic concurrency, pagination, and a transactional outbox for
emitting events exactly once per write. Backed by MongoDB today
(Postgres support in progress — see the
[Postgres backend](/docs/reference/postgres-rfc) page).

Speed doesn't stop at scaffolding: `testkit` gives every test an
isolated database and an HTTP client with no service to stand up, and
dry run lets a whole service run against real traffic with every write
rolled back — so a resource's actual flows, including the ones that
span several writes and an outbox event, get tested as themselves, not
as a mock of themselves.

You can describe that resource two ways. In Go:

```go
res := &rest.Resource{
	Name:  "tags",
	Store: env.Store("tags"),
	Schema: rest.Schema{
		"name": {Type: rest.TypeString, Required: true},
		"slug": {Type: rest.TypeString, Required: true, Unique: true},
	},
}
env.API.Register(res)
```

Or, for the common case, in a YAML file — no Go at all:

```yaml
resources:
  - name: tags
    collection: tags
    schema:
      name: {type: string, required: true}
      slug: {type: string, required: true, unique: true}
```

```bash
RESOURCES_FILE=resources.yaml go run ./cmd/hyrule-serve
```

Both register the same kind of resource, through the same `Resource`
type underneath. That's the whole idea: start from data, drop into Go
only for the parts that genuinely need code.

:::info[You will learn]
- What a `Resource` actually is, and what `Store` decouples it from
- How to add relations between resources, and how hooks run on a write
- How the transactional outbox lets a write and its side effects commit
  together — and how to make an event fire *because* of a write, not
  because some endpoint remembered to send it
- How to test a resource without standing up a whole service
- When a resource is better left as Go instead of YAML
:::

## The running example

This guide builds toward **blog-apis**, the small example that ships
with the framework: `authors-api` and `tags-api` are plain CRUD,
`categories-api` is a hierarchy, and `posts-api` ties them together with
relations, hooks, and an outbox-driven publish flow. Every snippet in
this guide is real code from that example or from the framework
itself — nothing here is invented for the docs.

If you'd rather read the example directly, it's at
[`examples/blog-apis`](https://github.com/Gui-Rondon/hyrule/tree/main/examples/blog-apis)
in the repository. If you'd rather be guided through it one concept at a
time, keep reading — start with [Quick Start](./quick-start.md).

:::tip[Recap]
- hyrule turns a resource description into a full CRUD API.
- A resource can be written in Go or in YAML; both paths are first-class.
- This guide uses blog-apis as one running example, building it up
  concept by concept.
:::
