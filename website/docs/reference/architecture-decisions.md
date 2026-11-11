---
sidebar_position: 10
---

# Architecture decisions

This page records decisions, not capabilities — it's the output of a
Phase 0 audit of the actual code (not a plan written from assumptions).
Each ADR's **Status** line tracks whether its decision has actually
been implemented and validated, separately from whether the decision
itself was accepted — accepting a direction and building it are two
different facts, and this page doesn't conflate them. See
[`roadmap.md`](roadmap.md) for how the phases that implement these
decisions sequence against each other.

## ADR-0001 — Public surface of the framework

**Status:** Implemented

**Implemented in:** PR #6 (Fase 1, commit `2385f9e`)

**Context.** Every package lives under `internal/`:
`app`, `rest`, `behaviors/*`, `outbox`, `platform/*`, `testkit`,
`declarative`, `domain/slug`. There is no `pkg/`. `examples/blog-apis`
imports `internal/app` directly, but only because it's inside the same
module — Go's `internal/` visibility rule means no external module can
import any of it today, regardless of what the README's pkg.go.dev
badge suggests.

**Problem.** A consumer can't `go get github.com/Gui-Rondon/hyrule` and
build anything with it.

**Alternatives considered.**
- Move core packages into a new `pkg/` directory — rejected; the
  project's own rule against creating `pkg/` "apenas por convenção"
  applies, and Go doesn't require it.
- Promote exactly the packages an external consumer needs — `rest`,
  `app`, `outbox`, `testkit`, `declarative`, `behaviors/*`, and the
  `platform/*` adapters a consumer constructs directly (`mongodb`,
  `postgres`, `redis`, `pubsub`, `auth`, `config`, `health`, `metrics`,
  `server`) — from `internal/` to the module root, same package names.
- Keep everything internal and build a separate public "SDK" package
  that re-exports a curated subset — rejected; doubles maintenance of
  the same surface and creates exactly the kind of parallel
  implementation the project is trying to avoid.

**Decision.** The second option. `domain/slug` stays internal until an
external need for it surfaces. This ADR records the destination; moving
the code is Fase 1, not this round.

**Consequences.** `cmd/hyrule-serve` and `examples/blog-apis` will need
their import paths updated when Fase 1 executes. The module's
compatibility surface grows from zero to everything exported by those
packages.

**Risks.** Large mechanical diff touching every file in the moved
packages; risk of exporting something from each package that should
have stayed implementation detail, without a deliberate per-package
pass to decide what's actually public.

**Validation criteria.** A separate scratch Go module (its own
`go.mod`) can `go get github.com/Gui-Rondon/hyrule` and build a minimal
resource using `rest`, `app`, `declarative`, and `platform/mongodb`,
without importing anything under `internal/`.

## ADR-0002 — Portability of the `rest.Store` contract

**Status:** Accepted — implementation deferred

**Implemented in:** not applicable; the decision itself is to defer the
generics rework (see Decision below). `rest.Store` is unchanged in
PR #6, as decided.

**Context.** `rest.Store`'s methods are typed in `bson.M`/`bson.D`, and
`rest.Service`'s error translation checks Mongo driver types directly
(`mongo.ErrNoDocuments`, `mongo.IsDuplicateKeyError`, `mongo.IsTimeout`
— see [`store.md`](store.md)). [`postgres-rfc.md`](postgres-rfc.md)
already decided, independently, that `postgres.Store` does not try to
implement `rest.Store`.

**Problem.** For ADR-0001's public promotion to mean anything for the
Postgres path, something has to stop the framework from silently
assuming Mongo-shaped errors once a consumer can actually choose a
backend.

**Alternatives considered.**
- Generify `rest.Store`/`Service`/`Resource` with Go generics
  (`Store[F, U any]`) — the bigger lift `postgres-rfc.md` names,
  touching most of `internal/rest/service.go`.
- Bypass `rest` entirely for Postgres-backed resources — the lower-cost
  path `postgres-rfc.md` already recommends, the same pattern
  `internal/outbox` already uses talking to `*mongo.Collection`
  directly.
- Keep `rest.Store` Mongo-shaped as the "reference" contract and build
  a second, independent HTTP/resource path for Postgres — rejected
  outright: this is two frameworks, and it's exactly the kind of
  Mongo/Postgres difference-hiding the project's own rules forbid
  papering over.

**Decision.** Defer the generics rework. Until a real Postgres-backed
resource is needed in production, the bypass path stays the supported
way to use `postgres.Store`, exactly as `postgres-rfc.md` recommends.
Fase 1 will not attempt to generify `rest.Store`. This ADR exists so
that's an explicit, revisitable decision instead of an implicit stall.

**Consequences.** The Framework journey's future "Storage and database
adapters" documentation can only honestly describe Mongo as the
`rest`-integrated backend; Postgres stays documented as a standalone,
lower-level package until the generics rework happens for real.

**Risks.** Blocks any near-term need for a `rest`-integrated,
non-Mongo resource. Revisit trigger: the first real resource that
needs both `rest`'s HTTP machinery and a non-Mongo store.

**Validation criteria.** No documentation describes a Postgres resource
going through `rest.Store`.

## ADR-0003 — Outbox and Postgres

**Status:** Accepted — implementation deferred

**Implemented in:** not applicable; the decision itself is to wait for
a real Postgres-backed resource (see Decision below). `outbox` (moved
from `internal/outbox` by ADR-0001/Fase 1) is unchanged in PR #6, as
decided.

**Context.** `internal/outbox` is hard-coded to `*mongo.Collection`/
`*mongo.Database`, writes a resource and its event in the same Mongo
transaction, and documents delivery as at-least-once, never
exactly-once. [`postgres-rfc.md`](postgres-rfc.md#9-beyond-parity-what-postgres-can-do-that-this-scaffold-doesnt-use-yet)
sketches a Postgres-native alternative (`BEGIN`/`COMMIT`,
`LISTEN`/`NOTIFY`, `FOR UPDATE SKIP LOCKED`) but says it depends on
ADR-0002 landing first.

**Problem.** Whether to start building outbox portability now,
speculatively, or wait for a real consumer.

**Alternatives considered.**
- Build a store-generic outbox interface now — rejected: there's no
  real consumer to validate the design against, which is exactly the
  "abstração genérica sem consumidores reais" the project rules out.
- Wait until ADR-0002 is revisited and a real Postgres-backed resource
  exists, then build the Postgres outbox counterpart driven by that
  resource's actual needs.

**Decision.** The second option. Outbox stays Mongo-only; no code
changes in this round or in Fase 1.

**Consequences.** The "two complementary, same-core paths" narrative
can't yet claim outbox portability — documentation should say so
plainly rather than imply it.

**Risks.** None from staying as-is. The only risk is building the
generic version before ADR-0002 unblocks it.

**Validation criteria.** No documentation or ADR claims a working
Postgres outbox until a resource actually uses one.

## ADR-0004 — Initial scope of the CLI / Platform

**Status:** Implemented

**Implemented in:** PR #6 (Fase 3, commit `fff7a61`)

**Context.** No multi-command CLI exists. `cmd/hyrule-serve` is a
single generic server binary. `ci/affected` already does real
build-affected detection as a standalone tool (it accepts `-services`
for layouts outside `examples/blog-apis`) but isn't wired into any CI
workflow or CLI. The `Makefile` already has `build`/`image`/`run`/
`env-up` targets parametrized by `SERVICE`.

**Problem.** The brief's Platform vision (`hyrule new`,
`service create`, `dev`, `test`, `build --affected`, `deploy`) is
large; building all of it before any of it has a real user risks
"implementar comandos apenas para preencher uma lista."

**Alternatives considered.**
- Build the full command surface in one pass.
- Start with only project/service scaffolding (`hyrule new`,
  `hyrule service create`), reusing the Makefile and `ci/affected` for
  everything build/test/affected-related instead of reimplementing them
  as Go subcommands immediately.
- Wrap `ci/affected` and the Makefile targets as subcommands first,
  deferring scaffolding.

**Decision.** Start scaffolding-only. A CLI's first real value is
removing hand-copying of `examples/blog-apis/services/*` when starting
a new service — `ci/affected`, the Makefile, and the one manifest
template already work and don't need a CLI wrapper to be useful today.
Wrap the existing tools as subcommands later, once the CLI exists as a
binary at all.

**Consequences.** The Platform journey's documentation should only
describe `hyrule new` and `hyrule service create` at first; `dev`/
`test`/`build`/`deploy` subcommands stay future work, and should be
marked as such wherever mentioned.

**Risks.** Scaffolding-only may feel small relative to the brief's full
vision — intentional; it's the smallest increment that's still real,
usable value.

**Validation criteria.** A person can run `hyrule new my-platform &&
hyrule service create articles` and get a service that builds and runs
via the existing Makefile targets, without hand-copying
`examples/blog-apis`.

## ADR-0005 — Versioning and compatibility pre-1.0

**Status:** Implemented

**Implemented in:** PR #6 (Fase 0, commit `b19f31c`); every phase
since has added its own `CHANGELOG.md` entry, in practice, not just on
paper.

**Context.** No `CHANGELOG`, one git tag (`v0.100.0`), no documented
compatibility policy. `go.mod` has no major-version suffix, consistent
with 0.x.

**Problem.** Fase 1 is about to introduce the repo's first real
breaking changes (moving `internal/` packages to public import paths)
with nowhere to record what changed or when.

**Decision.** Adopt the [Keep a Changelog](https://keepachangelog.com/)
format at `CHANGELOG.md`, and SemVer 0.x tags going forward (a breaking
change bumps the minor version, since the major stays `0`). Start the
changelog now, before Fase 1 executes, so its first entry has somewhere
to go.

**Consequences.** Future PRs that change public behavior should add a
changelog entry; no CI check enforces this in this round.

**Risks.** Low — purely additive.

**Validation criteria.** `CHANGELOG.md` exists, follows the Keep a
Changelog structure, and has an `Unreleased` section ready for Fase 1's
entries.

## ADR-0006 — Fase 4 operational scope: affected-build CI, not a deploy system

**Status:** Implemented

**Implemented in:** PR #6 (Fase 4, commit `b07df31`)

**Context.** `ci/affected` works (its own test exercises the real
`examples/blog-apis` dependency graph — see [`store.md`](store.md) and
the package doc of `ci/affected/main.go`) but no CI workflow calls it.
The only deploy artifact is one illustrative Kubernetes manifest
(`examples/blog-apis/deploy/posts-api/manifest.yml`), not wired to
anything, and specific to one provider's shape.

**Problem.** The brief's Fase 4 goals — "evoluir build-affected,"
"integrar build independente," "oferecer templates de CI/CD," "permitir
deploy independente com adapters configuráveis" — are easy to over-build
into a generic multi-cloud deploy system, which the brief's own rule 6
("não implemente um sistema universal de deploy") and the CI/CD rule
("não acople o Hyrule a um único provedor") explicitly warn against.
There's also no real multi-provider consumer yet to validate such an
abstraction against — the same "no real consumer" problem ADR-0003
named for outbox portability.

**Alternatives considered.**
- Build a provider-abstraction layer (interfaces for GitHub
  Actions/GitLab CI, for Kubernetes/Cloud Run/Fly/etc.) — rejected for
  the reason above: no real second provider to design against yet.
- Document the pattern in prose only, without a running workflow —
  rejected: "wire into an actual CI workflow" (the roadmap's own Fase 4
  wording) implies something that actually runs, not just a
  description of how it could.
- Add one real, running GitHub Actions workflow scoped to
  `examples/blog-apis` that calls `ci/affected` to select changed
  services, builds only those, and — the configurable seam — invokes an
  opt-in `deploy/<service>/deploy.sh` if the service has one, left as a
  stub that prints what it would do rather than touching a real
  environment. `deploy.sh` is the whole "adapter": any provider's real
  deploy command goes there, Hyrule never sees it.

**Decision.** The third option. Independent build is real (one `go
build` per affected service in CI, not a repo-wide rebuild); independent
deploy is a documented extension point (`deploy/<service>/deploy.sh`),
not a shipped capability — there's nothing to configure yet because
there's no real target environment for this public example to deploy
to. Per-service versioning (the brief's Fase 4 objective 4) is left
unstarted: these example services aren't independently published today,
and inventing a scheme with no real consumer repeats the mistake the
alternatives above were rejected for.

**Consequences.** This workflow is `examples/blog-apis`'s own concern —
illustrative, like its Kubernetes manifest — not a capability the
Hyrule framework or CLI promises as part of its public contract.
A team using `hyrule new` copies and adapts the pattern; Hyrule doesn't
ship a "deploy" subcommand that calls it.

**Risks.** Someone could mistake the workflow for a supported Hyrule
CI integration rather than a copyable template — mitigated by comments
in the workflow file and this ADR, the same honesty framing
[`postgres-rfc.md`](postgres-rfc.md) already uses.

**Validation criteria.** A push or PR that only touches one service's
files causes the workflow to report and build only that service, in
less time than a full rebuild; a push touching a shared path (go.mod,
`platform/*`, `Dockerfile`) causes it to report and build all of them.
Each service's `deploy.sh` stub runs without error and makes no real
external change.
