---
sidebar_position: 11
---

# Roadmap: framework and platform

This page records completed foundation work and proposed evolution. Phase
summaries distinguish implementation evidence from production adoption and formal
decision acceptance. See [architecture decisions](architecture-decisions.md).

## Current adoption and stability work

Phases 0–5 below are historical; Phase 6 has study evidence but remains open;
Phases 7–9 have local identity, discovery and MCP proofs; Phase 10 controlled MCP mutations remains proposed; Phase 11 has local retrieval, model evaluation, source/outbox and a concrete producer. Production adoption remains open. The current [v1 roadmap](https://github.com/Gui-Rondon/hyrule/blob/main/ROADMAP.md)
adds explicit compatibility/support policy, Go 1.26/1.27 PR gates, SQLite local
scaffolding, router composition and manual attested releases. See
[stability](stability.md) and [SQLite](sqlite.md) for current behavior and limits.
The historical coverage figures below are evidence from earlier phases; the
current runtime gate is 95%. No v1 release date is set.

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
| Framework (`go get`) | Consumable by any external Go app | `app`, `rest`, `outbox`, `testkit`, `declarative`, `behaviors/*`, `platform/*` are public; `internal/domain`, `internal/integration` and `internal/pgexplain` stay internal | **Closed** — Fase 1 (verified: a scratch external module imports `rest`, `app`, `declarative`, `platform/mongodb`) |
| `rest.Store` across backends | Portable between Mongo and Postgres | `rest.Adapter` (`rest/adapter.go`) in front of `Store`, implemented by `restpostgres` over `platform/postgres`; `examples/blog-apis/services/comments-api` is a real resource served end to end through it | **Closed** — ADR-0002's revision; see [`adapter.md`](adapter.md) |
| Outbox across backends | Transactional events | Mongo `outbox` and Postgres `outboxpostgres`, shared hooks, comments-api publishes through Pub/Sub | **Closed** — see [outbox](outbox.md) and [evidence](integration-review.md) |
| CLI / Platform | `hyrule new` / `service create` / `dev` / `build` / `deploy` | `cmd/hyrule` has `new` and `service create` (ADR-0004); `dev`/`build --affected`/`deploy` not started | **Partially closed** — Fase 3 scaffolding done; `ci/affected`/Makefile wrapping deferred on purpose |
| Affected-build / independent deploy | Orchestrated by the platform | `ci/affected` runs for real in `.github/workflows/blog-apis-affected.yml`; deploy is a documented `deploy/<service>/deploy.sh` seam, not a shipped capability (ADR-0006) | **Closed for the example, by design** — Fase 4; a real deploy adapter and per-service versioning stay unstarted until there's a real consumer |
| Dual-journey documentation | Learn (Framework) + Learn (Platform) + shared Reference | Both journeys exist (`/docs/learn`, `/docs/platform`), each describing only what's built | **Closed** — Fase 5 |
| ADRs / versioning | A formal decision and compatibility process | ADRs, compatibility policy and `CHANGELOG.md` exist | **Closed** — Fase 0 / ADR-0005; the new ADRs below remain proposed |

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
  doesn't exist yet. **Done.** A new "Platform" category under
  [Learn](/docs/learn/intro) — `/docs/platform/intro`,
  `creating-a-project`, `creating-services`, `local-development`, and
  `ci-and-deploy` — covers exactly `hyrule new`/`hyrule service
  create` and the `ci/affected`/`deploy.sh` pattern from Fase 4,
  naming every unbuilt command (`dev`, `build --affected`, `deploy`)
  as unbuilt rather than omitting them silently. English only, like
  the rest of Learn — see [`README.pt-br.md`](https://github.com/Gui-Rondon/hyrule/blob/main/README.pt-br.md)
  for that existing asymmetry.

## Historical round limits (Phases 0–5)

**Review note — 2026-11-16.** The paragraph below preserves the original scope,
not current capabilities. Outbox portability was implemented later, as recorded
in ADR-0003's revision. Fase 1's sole-internal-package statement is historical too;
`internal/integration` and `internal/pgexplain` exist today. The CLI gained local
SQLite, and YAML supports all three registered backends; this does not add outbox
or dry-run to SQLite. See the [current review](ai-readiness.md).

No `rest.Store` interface change, no outbox portability, no
`dev`/`build --affected`/`deploy` CLI subcommands (`ci/affected` runs in
CI now, but isn't wrapped by `cmd/hyrule`), no deploy-provider
abstraction, and no per-service versioning scheme. Those are exactly
the decisions the project's own rules say to stop and present before
executing — ADR-0002, ADR-0003, ADR-0004, and ADR-0006 are that
presentation for each of them.

## Proposed evolution — Fase 6+ (2026-11-16)

Phase 6 has a [completed study](phase-6-contract-study.md) and contract fixes;
formal decision acceptance remains open. Phases 7–8 have local JWT/policy,
bounded reads, authorized discovery and optional loopback HTTP helpers. Phase 9
has SDK MCP memory/stdio proofs; production client/remote transport adoption remains open.
Phase 10 controlled MCP mutations is not started. Phase 11 now has documentation
retrieval, local real-model evaluation, versioned Postgres source/outbox and a
[concrete article producer](knowledge-producer.md). Production rollout and broader
quality acceptance remain open. These increments do not mark all proposed
[ADRs 0007–0012](architecture-decisions.md) accepted or all phases Done. The v1
stabilization gates and historical Postgres portability numbering are independent.
Complexity is relative, with no staffing or schedule commitment.

| Phase | Problem / benefit | Complexity | Main risk | Dependencies | Done when |
|---|---|---|---|---|---|
| **6 — Contracts and DX** | Avoid parallel schemas; improve diagnostics and decide codegen from evidence | Medium | Growing into an ORM/compiler | ADR-0007; declarative and Go resource consumers | Contract comparison validated; unrepresentable rules documented; authoring source chosen per resource; generation or deferral justified by the study. |
| **7 — Safe executor and opt-in read** | Reuse `Service` while preserving identity, policy and limits | High | Cross-tenant access or handler bypass | ADR-0008/0010; real consumer and policy | One real bounded read registered explicitly; fail-closed identity/policy; limits, isolation, projection and REST parity verified. Tool writes remain disabled. |
| **8 — Context discovery** | Provide useful authorized contracts | Medium | Metadata/cache leakage | Phases 6–7; ADR-0009 | Deterministic scoped catalog; correct input/output and restrictions; isolated cache; no secrets or OpenAPI regression. |
| **9 — Read-only MCP pilot** | Validate interoperability with a real client | Medium/high | Protocol/session compatibility | Phases 7–8; ADR-0011; chosen SDK/version/transport | A real client lists and invokes the read; authentication/policy, errors, cancellation and isolation verified; entirely opt-in. |
| **10 — Controlled mutations** | Enable useful action with concurrency and traceability | High | Duplicate writes and external effects | Phases 7–9; write consumer; ADR-0010 complete | One real mutation preserves ETag, policy, validation/hooks and outbox where applicable; idempotency/bound approval verified; ambiguous timeout handled; dry-run advertised only where proved. |
| **11 — Conditional retrieval/RAG** | Improve knowledge retrieval if the baseline justifies it | High | Quality, cost and index leakage | ADR-0012; corpus/queries/ACL/budget; Mongo or Postgres outbox | Baseline/thresholds agreed; idempotent pipeline and reproducible evaluation pass; revocation/deletion and reindexing verified. Static documentation/pgvector/MCP pilot implemented; local real-model evaluation and versioned Postgres source/outbox implemented; broader quality acceptance and production producer wiring remain open. See [pilot](knowledge-pilot.md). |

Main sequence: 6 → 7 → 8 → 9 → 10. Phase 11 is independent of MCP and AI-driven
mutations; it can start after contracts, policy and a real retrieval consumer are
defined. Document search does not require an MCP server.

### Read pilot definition — 2026-11-16

The [tags.lookup pilot](read-pilot.md) selects a resource, input/output, limits
and proposed access policy for a local synthetic dataset. The standalone example's internal package now implements execution/discovery
authorized by injected callbacks, mandatory audit and real SQLite tests. Phase 7
remains partial: production provider configuration/transport, a production consumer and
production policy are still pending.

### Verification in future implementations

Reuse `testkit`, adapter suites and existing CI gates. New tests should prove
boundary invariants: authorization before execution, limits before querying,
validation/ETag/hook parity, identity isolation, absence of effects in advertised
modes and failure handling. Do not add tests that only mirror DTOs or descriptors.

The bounded read pilot can use SQLite within its supported capabilities. Outbox
and rollback proofs require backends that actually support them. The existing
95% gate alone is not proof of security. Verify external package consumption and
examples without AI credentials too.

### Explicitly open decisions

Production consumer/policy rollout; eventual codegen source/direction; neutral
contract-export API; package names/distribution; production MCP transport/client adoption;
approval/idempotency storage; broader RAG quality/load thresholds and production provider choices.
These choices require evidence in the corresponding phase; they do not prevent
review of the proposals.

The pilot now integrates RS256 JWT verification and scope policy with real
signature tests. A persistent SQLite audit sink is also available. Production provider
configuration and external transport remain pending; Phase 7 remains partial.

The opt-in local HTTP pilot now has JWT/audit-protected discovery and invocation,
real loopback tests and read/write limits. Remote deployment, production provider
configuration and remote MCP remain pending; Phase 7 remains partial.

Local pilot discovery now projects input/output schemas and contract limits with
a deterministic hash and per-call authorization/audit. Phase 8 remains partial:
generic catalog/profile cache remain open; the local MCP bridge is described below.

The [local MCP adapter](mcp-pilot.md) implements tools/list and tools/call through
the executor with SDK v1.8.0/protocol 2025-11-25 and real memory/stdio SDK clients.
Phase 9 remains partial for production consumer adoption and remote MCP.

The opt-in `cmd/tags-mcp` command now launches the pilot for stdio clients with
explicit configuration, read-only SQLite source and separate audit. The executable
is verified with a real SDK client; desktop/production adoption remains open.

### Documentation still needed for production adoption

The existing runbooks cover local configuration, grants, ingestion, source/outbox,
curator CRUD and evaluation. Production-specific documentation must follow actual
integration evidence:

- Selected MCP client setup and its tested transport/authentication matrix.
- Homelab deployment topology across setup-tools, risotto-site and hub, including
  resources, NetworkPolicies, routes, secrets and rollback.
- Authelia integration design: trusted identity mapping, token type/audience/scopes,
  key rotation and document authorization. ForwardAuth headers are not implicitly
  accepted by the current JWT verifier; this integration is not implemented.
- Deployment-specific backup/restore, audit/tombstone retention, worker/DLQ alerts
  and replay procedures, with recovery tests.
- Agreed quality/load/freshness thresholds beyond the recorded local experiment.

These are open documentation/integration deliverables, not claims that the local
pilot lacks the implemented source/outbox or article producer. Formal acceptance
of ADRs remains a review decision independent of implementation evidence.
