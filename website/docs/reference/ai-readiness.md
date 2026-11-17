---
sidebar_position: 21
---

# DX and AI-readiness review

**Review date:** 2026-11-16. **Baseline:**
`970b7f141a4eb7eb0865865191acd3d4ee53a9b7` of
`github.com/Gui-Rondon/hyrule`.

This review records the initial audit against the baseline above and extends
ADRs 0001–0006 without restarting completed work. Its original analysis did not
implement runtime features; the Phase 6 study used in-memory SQLite.

**Implementation update — 2026-11-17.** Later PR #70 increments added contract
fixes, local JWT/policy and discovery, SDK MCP/stdio, pgvector retrieval, a real-model
evaluation, a versioned source/outbox and a concrete article producer. See the
[ADR implementation table](architecture-decisions.md) and [roadmap](roadmap.md)
for current evidence and open scope. ADRs 0007–0012 remain formally Proposed;
Phase 10 controlled MCP mutations remains unimplemented. Production rollout,
generic registry/export APIs and broader quality acceptance remain open.

## Audit corrections

- Public packages, the `rest.Adapter` portability seam, Mongo/Postgres outbox,
  CLI scaffolding, affected-build CI and versioning already exist. Use them
  rather than proposing them again. Postgres implements a document contract
  over JSONB and metadata, not an arbitrary relational ORM.
- `declarative.Registry` has concrete Mongo, Postgres and SQLite implementations
  in `cmd/hyrule-serve/backends.go`. It is not an unused speculative abstraction.
  The process still owns one connected backend; the registry does not promise
  multi-database transactions.
- SQLite already supports YAML through `backend: sqlite`, subject to its
  [experimental limits](sqlite.md). Outbox, dry-run and advanced parity remain
  unsupported. The CLI's local default is SQLite; the platform layout remains
  available explicitly.
- The Go authoring path builds `Resource`/`Schema` explicitly. No general API
  inferring schema from arbitrary domain structs was found.
- `app/dryrun.go` configures deployment-level dry-run. Mongo's `WriteGuard`
  detects outside-transaction writes after they have persisted; it does not
  prevent them. Postgres has separate rollback composition. SQLite rejects
  dry-run. External effects from hooks cannot be rolled back by either database.
- HTTP authentication and request preparation belong to `rest.API`/handlers;
  invoking `Service` directly does not reapply that perimeter.
- `platform/search/elasticsearch.go` provides indexing/update/delete and a
  healthcheck, not semantic retrieval. `behaviors/searchsync` is post-commit
  best-effort, not durable index synchronization.
- `internal/domain`, `internal/integration` and `internal/pgexplain` remain
  internal today. Older phase descriptions record the earlier tree.

The historical roadmap also contained stale statements about versioning and
outbox. Dated notes preserve the original decisions while identifying the current
state. Existing CI gates (including 95% runtime coverage), security workflows and
release provenance should be reused. Reading a workflow does not verify remote
branch protection or the latest release result. The root v1 roadmap remains
independent; AI work is not implicitly a new v1 gate.

## DX design comparison

This is a design comparison, not a throughput or productivity benchmark. Hyrule
includes validation, REST semantics, hooks and concurrency checks; comparing a
complete request with an isolated SQL call would not measure equivalent work.

| Reference | Documented approach | Useful adaptation | Boundary |
|---|---|---|---|
| Bun | Go models and SQL-first development | Query diagnostics and explicit application SQL paths | Do not hide relational joins behind supposedly universal document filters. |
| Ent | Schema-driven generation of statically typed APIs | Deterministic generation and drift checks when consumers show recurring duplication | Do not import a relationship graph, migrations and eager loading into the REST core. |
| GORM | Structs, tags and configurable conventions | Small examples, explicit defaults and documented escape hatches | Do not infer production migrations or introduce hidden associations. |
| sqlc | Typed code generated from schema and SQL queries | Preserve SQL visibility for relational workloads outside the document contract | Do not replace `Adapter` or turn arbitrary SQL into generic REST operations. |

Primary sources: [Bun introduction](https://bun.uptrace.dev/guide/getting-started.html),
[Bun models](https://bun.uptrace.dev/guide/models.html),
[Ent code generation](https://entgo.io/docs/code-gen/),
[GORM models](https://gorm.io/docs/models.html),
[GORM conventions](https://gorm.io/docs/conventions.html),
and [sqlc generation](https://docs.sqlc.dev/en/v1.26.0/howto/generate.html).
The sqlc reference is versioned 1.26.0; this review uses its generation principle
without claiming it is the latest release.

Hyrule already combines schema, validation, hooks, ETags and preconditions in a
resource contract. YAML and Go definitions converge on the same runtime types.
SQLite lowers the infrastructure cost of initial exploration. The next justified
step is contract consistency and diagnostics; a complete entity generator still
needs measured evidence. [ADR-0007](architecture-decisions.md) proposes that study.

## Optional composition

```text
Application / composition root
  +-- rest.API -----------------------> rest.Service --> rest.Adapter
  +-- optional MCP adapter
        +-- tool catalog / discovery
              +-- authorized executor -> rest.Service

External descriptors -> contracts derived from Resource/Schema
Existing hooks ------> domain audit / transactional outbox
Optional worker -----> embeddings / index / retrieval
```

This diagram describes the target composition. Local executors, MCP adapters
and the source/index worker are implemented in the optional standalone module;
the general reusable catalog/export API remains proposed. Tool registration is explicit; hooks handle lifecycle effects while a wrapper handles authorization,
limits and dispatch. AI metadata references the existing resource contract.
`platform/*` never imports `rest`; neither `rest` nor `app` imports MCP/model SDKs.
Consider a separate submodule if root-module dependency isolation is required;
package separation alone does not guarantee it.

The current local consumers supply explicit resources, bounded reads and access
policies. Production MCP exposure requires its own client/transport validation. Add
mutations only after concurrency, idempotency, approval and external effects are
validated. Retrieval is an independent optional branch; it does not require MCP.

## Open choices and compatibility

The local consumers, SDK v1.8.0/protocol 2025-11-25, pgvector backend and a local
embedding evaluation are selected and implemented. Remaining choices concern
production consumers/policy and transport/client adoption, broader model/quality
acceptance, a neutral public exporter/registry, eventual codegen and durable
approval/idempotency storage. The exploratory measurement does not establish a
production quality gate or select infrastructure for the homelab deployment.

The direction is additive: external descriptors, optional packages and explicit
composition. No signature change to `rest.Adapter`, `rest.Store`, `Service` or
`app.Spec` is proposed. A neutral contract-export API, if required, is new public
surface subject to review. Preserve BSON compatibility and backend-specific query
semantics. Continue the existing compatibility/changelog process for any future
public behavior change.

## Evidence

The review read the ADR and roadmap pages, `ROADMAP.md`, `app/app.go`,
`app/dryrun.go`, `testkit/testkit.go`, `rest/{resource,schema,service,http,adapter,
store,mongoadapter,openapi,swagger}.go`, `declarative/{declarative,backend}.go`,
`cmd/hyrule-serve/backends.go`, `restpostgres/`, `restsqlite/`,
`behaviors/{events,searchsync}/`, `outbox/`, `outboxpostgres/`,
`platform/search/elasticsearch.go`, the SQLite reference and existing workflows.
Historical implementation evidence remains in [the integration review](integration-review.md).
The initial review claimed no new CI/performance/MCP evidence. Later increments
record a [client matrix](mcp-pilot.md), [model evaluation](knowledge-evaluation.md)
and executable integration tests in the standalone module.

## Phase 6 study results

The [contract study](phase-6-contract-study.md) executed 21 cases with the real
SQLite adapter. Six structural-schema/HTTP differences were reproduced; codegen
remains deferred. This evidence does not imply acceptance of the proposed ADRs
or completion of all Phase 6 criteria.

The C6-01 follow-up preserves nested required fields in the OpenAPI PATCH
contract; the study records both the original measurement and the post-fix rerun.
Server-side constraints C6-02–04 remain explicit.

The [tags.lookup read pilot](read-pilot.md) defines the resource and proposed
policy for the next increment, including limits and acceptance criteria.

The optional [knowledge pilot](knowledge-pilot.md) implements the selected documentation corpus with pgvector, explicit subject ACLs and MCP search/fetch; the local [source/outbox path](knowledge-sync.md) is now implemented, while broader producer integration and semantic-quality acceptance remain open.
