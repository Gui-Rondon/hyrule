---
sidebar_position: 3
---

# Creating services

:::info[You will learn]
- How `hyrule service create` scaffolds a service
- What it generates, and why it's a starting point, not a finished API
- How what it generates relates to everything in [Learn](/docs/learn/intro)
:::

## Scaffold a service

From the root of a project `hyrule new` created (it looks for a
`services/` directory, so this fails outside one):

```bash
hyrule service create articles
```

This writes `services/articles/`:

- `main.go` — `app.Main(spec())`, the same bootstrap
  [Your First Resource](/docs/learn/your-first-resource) builds by
  hand.
- `resource.go` — a `setup` function registering one `rest.Resource`
  named `articles`, with a minimal schema (one required `name` field)
  and `behaviors/audit`'s hooks already wired in.
- `resource_test.go` — one `testkit`-based test exercising a create and
  a get, the same pattern [Testing](/docs/learn/testing) walks through.

## It's a starting point, not a finished resource

What's generated is deliberately small: one field, two methods
(`GET`/`POST` on the collection, `GET`/`PATCH` on an item), one
behavior. From here, everything in [Learn](/docs/learn/intro) applies
exactly as written — there's no Platform-specific resource format to
learn on top of it:

- Add fields and validation to `Schema` — see
  [Your First Resource](/docs/learn/your-first-resource) and
  [Reference → `resources.yaml`](/docs/reference/resources-yaml) if
  you'd rather declare it as data.
- Add relations to another service's resource, or a hook — see
  [Relations and Hooks](/docs/learn/relations-and-hooks).
- Wire the transactional outbox — see
  [The Transactional Outbox](/docs/learn/the-outbox).

`hyrule service create` only saves you the boilerplate of the first
three files; what you build on top is the same framework, used the
same way, whether or not the CLI wrote the first line.

:::tip[Recap]
- `hyrule service create <name>` writes a minimal, working
  `app.Main` + `rest.Resource` + test — not a finished API.
- Everything the Learn guide teaches about resources, hooks, relations
  and the outbox applies unchanged to a generated service.
:::
