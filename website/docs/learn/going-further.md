---
sidebar_position: 7
---

# Going Further

You've now seen the same framework from both ends: a resource built
entirely from a YAML file in [Quick Start](./quick-start.md), and one
built up in Go, field by field, hook by hook, in the pages since. This
page is about choosing between them — and where to go once you're
building something of your own.

:::info[You will learn]
- A concrete rule for when a resource can stay YAML
- That the two paths aren't exclusive — they share a process
- Where the Reference section picks up from here
:::

## Go or YAML?

The rule is simpler than it sounds: **if a resource's `setup` function
would contain nothing but a `Resource` literal and one `Register` call,
it can be YAML.** `authors-api` and `tags-api` both qualify — you saw
`authors-api`'s full `setup` in [Your First Resource](./your-first-resource.md);
it's a schema and a `Register`, nothing else.

`posts-api` can't be YAML, because its `setup` does things YAML has no
way to express: the `/publish` and `/schedule` endpoints, the
`outbox.Condition` from [The Transactional Outbox](./the-outbox.md), the
`draftHook` from [Relations and Hooks](./relations-and-hooks.md). None
of that is a limitation to work around — `Field.Check`, `Resource.Validate`,
and every kind of `Hooks` are left out of the YAML loader on purpose,
because they carry behavior, not data. A YAML file can't express "run
this code"; that's what Go is for.

## Both in the same process

A resource built from YAML is a plain `*rest.Resource` — the same
type `authors-api`'s hand-written `setup` builds. Nothing stops a single
service from registering some resources from a YAML file and others by
hand, or from taking a YAML-built resource and attaching hooks to it in
Go before registering it.
`declarative/declarative_test.go`'s
`TestBuiltResourceComposesWithConditionalOutbox` does exactly that: parses
a resource from YAML, then attaches the same conditional outbox hook from
[The Transactional Outbox](./the-outbox.md) to it, in Go — proving the two
paths aren't two different frameworks wearing the same name.

## A few things this guide skipped

- **Dry run**: a service can run against real traffic, writes included,
  with every transaction rolling back — useful to validate a new
  version before it gets the final word. See
  [Reference → dry run](/docs/reference/dry-run).
- **Observability**: every service exposes Prometheus metrics at
  `/metrics` and `/healthcheck` for free. See
  [Reference → observability](/docs/reference/observability).
- **Why the layers are shaped this way**: `services → behaviors →
  rest → platform`, and why `platform` never imports `rest`
  even when it would save a line. See
  [Reference → architecture](/docs/reference/architecture).

## Where to go from here

The Reference section covers every field and option this guide
simplified for the sake of a narrative — start at
[Reference → resources.yaml](/docs/reference/resources-yaml) if you're
about to write a real resource, or
[Reference → Store](/docs/reference/store) if you're curious how far
the storage interface actually decouples things today. The
[`examples/blog-apis`](https://github.com/Gui-Rondon/hyrule/tree/main/examples/blog-apis)
directory is every snippet in this guide, in context, with its own
tests — reading it end to end is the fastest way to see the whole
framework at once.

:::tip[Recap]
- A resource stays YAML as long as its `setup` would only build a
  `Resource` and register it; anything with hooks, custom endpoints, or
  custom validation is Go.
- Both paths build the same `Resource` type and register the same way —
  they compose in a single process.
- The Reference section is where the detail this guide skipped lives.
:::
