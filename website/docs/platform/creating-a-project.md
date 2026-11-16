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

`@main` installs the main branch. To pin a published release, replace it
with the desired tag. See the [roadmap](/docs/reference/roadmap) for the CLI scope.

## 2. Scaffold the project

```bash
hyrule new my-platform
cd my-platform
```

This writes:

- `go.mod` — just `module my-platform` and a Go version; it doesn't
  pre-require Hyrule, so you can add the dependency at the version you choose.
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

This is an ordinary public Go module dependency. Use `@main` for the main
branch or a release tag to pin a version. For a local checkout, see the
`replace` directive in
[`examples/standalone-tags-api`](https://github.com/Gui-Rondon/hyrule/tree/main/examples/standalone-tags-api).

:::tip[Recap]
- `hyrule new <project>` generates `go.mod`/`Makefile`/
  `docker-compose.yml`/`services/` — plain Go project layout, not a
  custom format.
- Add Hyrule with `go get` at the version you choose.
:::
