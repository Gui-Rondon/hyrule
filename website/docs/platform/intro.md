---
sidebar_position: 1
---

# Introduction to the Hyrule Platform

Everything in [Learn](/docs/learn/intro) builds one resource, one
service, one `go.mod` at a time — the **Framework** path. The
**Platform** path is for a team that wants several of those services
in one repository, with the boilerplate — `go.mod`, `Makefile`,
local infra, a starting resource — generated instead of hand-copied.
Both paths build the exact same `rest.Resource`, through the exact
same `app.Main`; Platform is tooling around the Framework, not a
second implementation of it.

:::info[You will learn]
- How `hyrule new` scaffolds a multi-service project
- How `hyrule service create` adds a service to it
- How to run and test what gets generated, with the same
  `make env-up`/`make test` commands Quick Start already used
- How to copy `examples/blog-apis`'s affected-build CI and deploy
  pattern into your own project
:::

## What's real today, and what isn't

This page, and the ones after it, describe exactly two commands:

```bash
hyrule new <project>
hyrule service create <name>
```

That's the whole CLI right now ([ADR-0004](/docs/reference/architecture-decisions)).
There's no `hyrule dev`, no `hyrule build --affected`, no `hyrule
deploy` — those stay on the [roadmap](/docs/reference/roadmap) until a
real need justifies them, rather than existing to fill out a command
list. What those commands *would* wrap already works on its own,
though, and this journey shows you how to use it directly:
[build-affected detection](/docs/reference/architecture-decisions)
and a configurable deploy seam both run for real in
[`examples/blog-apis`](https://github.com/Gui-Rondon/hyrule/tree/main/examples/blog-apis)'s
own CI — [CI and Deploy](./ci-and-deploy.md) walks through copying
that pattern into a project `hyrule new` generated for you.

## The shape of a generated project

```
my-platform/
├── go.mod              # requires github.com/Gui-Rondon/hyrule
├── Makefile             # build/run/test/env-up, same shape as this repo's own
├── docker-compose.yml   # Mongo, replica set, local dev
├── README.md
└── services/
    └── articles/        # hyrule service create articles
        ├── main.go
        ├── resource.go
        └── resource_test.go
```

Nothing here is Platform-specific data format or a custom DSL — it's
the same `go.mod`/`Makefile`/`rest.Resource` a Framework-only user
writes by hand, generated instead of typed out. That's deliberate: a
generated project is never locked into the CLI that created it.

:::tip[Recap]
- Platform is tooling around the Framework path, not a second one.
- Today's CLI is exactly `hyrule new` and `hyrule service create` —
  everything else is either not built yet, or already works without a
  CLI wrapper (CI/deploy — see the last page of this journey).
:::
