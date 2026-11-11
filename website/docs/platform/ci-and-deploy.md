---
sidebar_position: 5
---

# CI and deploy: the pattern to copy

:::info[You will learn]
- Why this is "copy a pattern," not "run a command"
- How `ci/affected` decides which services a change touched
- Where a real deploy command goes, and why Hyrule doesn't ship one
:::

## There's no `hyrule build --affected` or `hyrule deploy` yet

Those commands aren't implemented ([ADR-0004](/docs/reference/architecture-decisions)),
and there's no generic deploy system behind them to call either —
building one before a real multi-provider need exists is exactly what
[ADR-0006](/docs/reference/architecture-decisions)
decided *against*. What exists instead is a real, running example you
copy into your own project: [`examples/blog-apis`](https://github.com/Gui-Rondon/hyrule/tree/main/examples/blog-apis)'s
own CI.

## The pattern

[`ci/affected`](https://github.com/Gui-Rondon/hyrule/tree/main/ci/affected)
is a small Go program, already part of the `github.com/Gui-Rondon/hyrule`
module: given a base commit, it prints which services' *binaries*
actually changed — a service is affected when one of its own files
changed, when a file under its `deploy/<service>/` changed, or when a
module it depends on was bumped in `go.mod`/`go.sum`. A change to
something every service shares (the Go version, a `Dockerfile`)
affects all of them.

[`.github/workflows/blog-apis-affected.yml`](https://github.com/Gui-Rondon/hyrule/blob/main/.github/workflows/blog-apis-affected.yml)
calls it for real on every push and pull request against this
repository:

```bash
go run ./ci/affected "$base"
```

— then builds only the services that came back, and calls each one's
`deploy/<service>/deploy.sh` if it has one.

## Copying it into your project

1. Your `services/` directory is already in the shape `ci/affected`
   expects (one subdirectory per service, matching
   `go build ./services/<name>`) — [Creating services](./creating-services.md)
   wrote it that way.
2. Vendor or `go run` the tool from the module you already depend on:
   `go run github.com/Gui-Rondon/hyrule/ci/affected -services services
   "$base"` (`-services` points it at your project's directory instead
   of `examples/blog-apis/services`, the default).
3. Copy `blog-apis-affected.yml`, adjusting the paths, as a starting
   workflow.
4. For each service that needs independent deploy, add
   `deploy/<service>/deploy.sh` — a plain script, executable, that does
   whatever your provider needs (`kubectl apply`, `flyctl deploy`,
   `gcloud run deploy`, ...). `ci/affected` only decides *whether* it
   runs; what's inside it is entirely yours, and Hyrule never sees it.
   `examples/blog-apis/deploy/*/deploy.sh` are stubs — they print what
   they would do, since this public example has nowhere real to
   deploy to.

## Why this stays outside the framework and the CLI

A deploy adapter abstraction (one interface, several providers behind
it) is the obvious next step to reach for — and the one [ADR-0006](/docs/reference/architecture-decisions)
explicitly didn't take, for the same reason [ADR-0003](/docs/reference/architecture-decisions)
didn't build outbox portability ahead of a Postgres resource that
needed it: there's no second real provider yet to design the
abstraction against. A script you own is a worse abstraction than a
good interface, but a better one than a guessed interface with one
real implementation.

:::tip[Recap]
- `ci/affected` already works and is already part of the module you
  depend on — no CLI wrapper required to use it today.
- `examples/blog-apis`'s workflow is a template to copy and adjust
  `-services`/paths for, not a Hyrule feature to configure.
- `deploy/<service>/deploy.sh` is the entire deploy "adapter" — your
  provider's command, nothing Hyrule-specific.
:::
