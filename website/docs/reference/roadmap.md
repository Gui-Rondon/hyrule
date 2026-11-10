---
sidebar_position: 11
---

# Roadmap: framework and platform

This page is a plan, not a feature list — most phases below are
proposed work, not something already built; each one says explicitly
when that changes. It exists so the
[Architecture decisions](architecture-decisions.md) have a sequence to
sit in, and so nobody mistakes a future phase for current behavior.

## Why two paths

The project's direction is to offer Hyrule two complementary ways to
adopt it, on the same core: a **Framework** path (`go get`, no
monorepo or CLI required) and a **Platform** path (a monorepo, CLI,
and scaffolding, for teams that want a standardized multi-service
setup). As of Fase 1, the Framework path is externally consumable (see
ADR-0001), and as of Fase 3, the Platform path has its first two
commands (see ADR-0004) — both gap rows below are kept for history, not
because they're still open.

## Gap analysis

| Area | Vision | Actual state today | Gap |
|---|---|---|---|
| Framework (`go get`) | Consumable by any external Go app | `app`, `rest`, `outbox`, `testkit`, `declarative`, `behaviors/*`, `platform/*` are public; only `internal/domain` stays internal | **Closed** — Fase 1 (verified: a scratch external module imports `rest`, `app`, `declarative`, `platform/mongodb`) |
| `rest.Store` across backends | Portable between Mongo and Postgres | Mongo types leak into the contract | **High** — needs a design decision before any code moves (ADR-0002) |
| Outbox across backends | Implied by the dual-mode story | Hard-coded to Mongo | **High**, correctly blocked until the Store question is resolved (ADR-0003) |
| CLI / Platform | `hyrule new` / `service create` / `dev` / `build` / `deploy` | `cmd/hyrule` has `new` and `service create` (ADR-0004); `dev`/`build --affected`/`deploy` not started | **Partially closed** — Fase 3 scaffolding done; `ci/affected`/Makefile wrapping deferred on purpose |
| Affected-build / independent deploy | Orchestrated by the platform | `ci/affected` runs for real in `.github/workflows/blog-apis-affected.yml`; deploy is a documented `deploy/<service>/deploy.sh` seam, not a shipped capability (ADR-0006) | **Closed for the example, by design** — Fase 4; a real deploy adapter and per-service versioning stay unstarted until there's a real consumer |
| Dual-journey documentation | Learn (Framework) + Learn (Platform) + shared Reference | Only the Framework journey exists, and it's already honest about what's missing | **Medium** — don't write the Platform journey until the CLI is real |
| ADRs / versioning | A formal decision and compatibility process | Didn't exist before this page | **High gap, low cost** — pure documentation |

## Phases

- **Fase 0 (this round).** Diagnosis, ADRs, and this roadmap, published.
  Risk: none (documentation only). Effort: low. **Done.**
- **Fase 1 — Consolidate the framework.** Execute ADR-0001 (promote the
  packages a consumer needs out of `internal/`) and hold the line on
  ADR-0002 (no `rest.Store` generics yet). **Done.** `app`, `behaviors/*`,
  `declarative`, `outbox`, `platform/*`, `rest`, and `testkit` moved to
  the module root (`internal/domain` is the only package left internal);
  `go build ./...`, `go vet ./...`, and `make cover` (90.9%, gate 85%)
  all pass, and a scratch external module building against the new
  paths compiles and runs. `rest.Store` itself is unchanged, per
  ADR-0002.
- **Fase 2 — Validate the standalone mode.** Build a real external
  example: a separate Go module, its own `go.mod`, importing published
  Hyrule — not another package inside `examples/blog-apis`. **Done.**
  [`examples/standalone-tags-api`](https://github.com/Gui-Rondon/hyrule/tree/main/examples/standalone-tags-api)
  is its own module (`replace` to this checkout until a version is
  tagged — see its README), with a YAML-declared resource, one Go hook
  (`behaviors/audit`), a `testkit`-based test, and a real `go run .` +
  `curl` cycle, all verified.
- **Fase 3 — Platform / CLI.** Implement the smallest useful CLI per
  ADR-0004. **Done, scaffolding only.** `cmd/hyrule` (`go install
  github.com/Gui-Rondon/hyrule/cmd/hyrule`) has `hyrule new <project>`
  and `hyrule service create <name>`, embedding templates for a
  project's `go.mod`/`Makefile`/`docker-compose.yml` and a service's
  `app.Main` + one resource + a `testkit` test. Verified end to end:
  `hyrule new` → `hyrule service create` → `go mod tidy` → `make build`
  → `make test` → `make run` + `curl` all work, with no hand-copying of
  `examples/blog-apis`. `ci/affected` and the Makefile pattern are
  *not* wrapped as subcommands yet — ADR-0004 deferred that on purpose
  until there's a second command that actually needs them.
- **Fase 4 — Operational experience.** Per ADR-0006. **Done, scoped
  to the example.** `.github/workflows/blog-apis-affected.yml` runs
  `ci/affected` for real on push/PR, builds only the services it
  reports, and calls each one's `deploy/<service>/deploy.sh` — a stub
  today, the seam any provider's real deploy command goes in. No
  provider abstraction, no generic deploy system, and no per-service
  versioning scheme: none of those have a real consumer yet to design
  against (see ADR-0006). This lives in `examples/blog-apis`, not as a
  Hyrule-shipped capability — a team using `hyrule new` copies and
  adapts it.
- **Fase 5 — Documentation.** Write the Platform journey only once the
  CLI is real, so the published site never describes a command that
  doesn't exist yet.

## What this round does not do

No `rest.Store` interface change, no outbox portability, no
`dev`/`build --affected`/`deploy` CLI subcommands (`ci/affected` runs in
CI now, but isn't wrapped by `cmd/hyrule`), no deploy-provider
abstraction, and no per-service versioning scheme. Those are exactly
the decisions the project's own rules say to stop and present before
executing — ADR-0002, ADR-0003, ADR-0004, and ADR-0006 are that
presentation for each of them.
