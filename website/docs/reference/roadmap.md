---
sidebar_position: 11
---

# Roadmap: framework and platform

This page is a plan, not a feature list — every phase below is
proposed work, not something already built. It exists so the
[Architecture decisions](architecture-decisions.md) have a sequence to
sit in, and so nobody mistakes a future phase for current behavior.
Phase 0 is the only phase this round actually completes.

## Why two paths

The project's direction is to offer Hyrule two complementary ways to
adopt it, on the same core: a **Framework** path (`go get`, no
monorepo or CLI required) and a **Platform** path (a monorepo, CLI,
and scaffolding, for teams that want a standardized multi-service
setup). Today only an informal version of the Framework path exists,
and it isn't externally consumable yet (see ADR-0001). The Platform
path doesn't exist beyond a few reusable building blocks
(`ci/affected`, Makefile targets, one example monorepo).

## Gap analysis

| Area | Vision | Actual state today | Gap |
|---|---|---|---|
| Framework (`go get`) | Consumable by any external Go app | Everything lives under `internal/` | **Critical** — nothing is importable today |
| `rest.Store` across backends | Portable between Mongo and Postgres | Mongo types leak into the contract | **High** — needs a design decision before any code moves (ADR-0002) |
| Outbox across backends | Implied by the dual-mode story | Hard-coded to Mongo | **High**, correctly blocked until the Store question is resolved (ADR-0003) |
| CLI / Platform | `hyrule new` / `service create` / `dev` / `build` / `deploy` | No subcommand exists | **Critical**, but reusable pieces already exist (`ci/affected`, Makefile, example monorepo) |
| Affected-build / independent deploy | Orchestrated by the platform | `ci/affected` works standalone, not wired into CI; one illustrative Kubernetes manifest | **Medium** — the hard part exists, the orchestration doesn't |
| Dual-journey documentation | Learn (Framework) + Learn (Platform) + shared Reference | Only the Framework journey exists, and it's already honest about what's missing | **Medium** — don't write the Platform journey until the CLI is real |
| ADRs / versioning | A formal decision and compatibility process | Didn't exist before this page | **High gap, low cost** — pure documentation |

## Phases

- **Fase 0 (this round).** Diagnosis, ADRs, and this roadmap, published.
  Risk: none (documentation only). Effort: low. **Done.**
- **Fase 1 — Consolidate the framework.** Execute ADR-0001 (promote the
  packages a consumer needs out of `internal/`) and hold the line on
  ADR-0002 (no `rest.Store` generics yet). Risk: high — this is a
  breaking, identity-affecting change and needs explicit approval
  before it starts, per the project's own rule about pausing before
  decisions like this.
- **Fase 2 — Validate the standalone mode.** Build a real external
  example: a separate Go module, its own `go.mod`, importing published
  Hyrule — not another package inside `examples/blog-apis`. Depends on
  Fase 1.
- **Fase 3 — Platform / CLI.** Implement the smallest useful CLI per
  ADR-0004 (`hyrule new`, `hyrule service create`), reusing
  `ci/affected` and the Makefile rather than reimplementing them.
  Risk: medium. Effort: high.
- **Fase 4 — Operational experience.** Wire `ci/affected` into an
  actual CI workflow; make deploy configurable rather than generic.
  Depends on Fase 3.
- **Fase 5 — Documentation.** Write the Platform journey only once the
  CLI is real, so the published site never describes a command that
  doesn't exist yet.

## What this round does not do

No code under `internal/` moves. No interface changes. No CLI gets
written. Those are exactly the decisions the project's own rules say to
stop and present before executing — this roadmap is that presentation.
