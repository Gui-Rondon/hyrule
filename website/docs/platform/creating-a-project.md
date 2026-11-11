---
sidebar_position: 2
---

# Creating a project

:::info[You will learn]
- How to install the `hyrule` CLI
- What `hyrule new` generates, and why
- How to point the generated `go.mod` at Hyrule
:::

## 1. Install the CLI

```bash
go install github.com/Gui-Rondon/hyrule/cmd/hyrule@main
```

Hyrule isn't tagged yet (see the [roadmap](/docs/reference/roadmap)), so
`@main` is today's answer; once a version is released, install that
instead (`@v0.101.0`, for example).

## 2. Scaffold the project

```bash
hyrule new my-platform
cd my-platform
```

This writes:

- `go.mod` — just `module my-platform` and a Go version; it doesn't
  pre-require Hyrule, because pinning a fake version would be a lie
  about something that doesn't exist yet.
- `Makefile` — `build`, `run`, `test`, `env-up`, `env-down`, the same
  shape as this repository's own.
- `docker-compose.yml` — a single-node Mongo replica set on
  `localhost:27017`, enough for `rest.Store` and the outbox's
  transactions.
- `README.md` and `.gitignore`.
- `services/` — empty; [Creating services](./creating-services.md) is
  next.

## 3. Add Hyrule as a dependency

```bash
go get github.com/Gui-Rondon/hyrule@main
```

This is an ordinary `go get` of a public Go module — nothing about it
is Platform-specific. If you'd rather see this step working end to end
against a checkout instead of the public repository, that's exactly
what [`examples/standalone-tags-api`](https://github.com/Gui-Rondon/hyrule/tree/main/examples/standalone-tags-api)'s
`go.mod` demonstrates, with a `replace` directive in place of the real
`go get` for the same not-tagged-yet reason.

:::tip[Recap]
- `hyrule new <project>` generates `go.mod`/`Makefile`/
  `docker-compose.yml`/`services/` — plain Go project layout, not a
  custom format.
- Hyrule isn't tagged yet, so the dependency is added with
  `go get ...@main` today.
:::
