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

**Status:** Accepted, then revised (see Revisão below) — the generics
rework stays deferred, exactly as decided; a different seam (`Adapter`)
is what actually shipped.

**Implemented in:** not applicable at the time this ADR was written
(the decision was to defer the generics rework, see Decision below);
`rest.Store` was unchanged in PR #6, as decided. See Revisão for what
was implemented instead, starting PR #10.

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

### Revision — 2026-11, Fase 8 of the Postgres portability plan

**What changed.** A real Postgres resource turned out to be needed
before the generics gap got revisited on its own — this ADR's own
revisit trigger ("the first real resource that needs both `rest`'s
HTTP machinery and a non-Mongo store"). None of the three original
alternatives was followed as written: not the generics rework
(`Store[F, U any]`), not bypassing `rest` entirely (the original
recommendation), not a second, independent HTTP path (rejected from
the start). What actually got built, approved explicitly by the user
("I agree that this design — a shared core via Adapter — is the right
path") before Fase 0 started: a fourth option, a new interface seam —
`rest.Adapter` (`rest/adapter.go`) — that `rest.Service` started using
instead of `rest.Store` directly.

**Why not generics.** `Adapter` doesn't parameterize `Store` by type —
it's a common interface each backend implements its own way
(`mongoAdapter` in `rest/mongoadapter.go`, `restpostgres`'s
`postgresAdapter`), covering exactly the surface `rest.Service` needs:
parsing/combining `where`/`sort`/`projection` (`QueryAdapter`), id
generation/parsing (`IDCodec`), error classification
(`ErrorClassifier`), and CRUD/merge-patch/uniqueness. The first three
were extracted out of `rest/service.go`/`query.go`/`http.go` back in
Fase 0 (zero Mongo behavior change, confirmed by a full regression
run) as separate, independently testable interfaces; Fase 4a unified
them into one `Adapter`, together with the write methods.
**`rest.Store` stays exactly what this ADR decided** — still
`bson.M`/`bson.D`-typed, still only implemented by
`platform/mongodb.Store` — because `Adapter` sits in front of it, not
in its place: a `Resource` with `Adapter` set never touches `Store`
at all (`Resource.setDefaults`, `rest/resource.go`, only builds a
`mongoAdapter` wrapping `Store` when `Adapter` is `nil` — the default
path for a Mongo resource that never had to change anything).

**What this enabled.** `restpostgres.NewAdapter` (a new package, the
only one importing `rest` and `platform/postgres` together, section 2
of the portability plan) implements `Adapter` in full over a `jsonb`
table plus relational metadata columns — see
[`adapter.md`](adapter.md) for the full contract and
[`examples/blog-apis/services/comments-api`](https://github.com/Gui-Rondon/hyrule/tree/main/examples/blog-apis/services/comments-api)
for the first real resource served end to end over HTTP through this
path (Fase 7). The `platform/postgres` scaffold this ADR already
named keeps existing, still never importing `rest`, exactly as
decided — `restpostgres` is the only bridge between the two, never
the other way around.

**Consequence for the documentation.** The original sentence ("the
bypass path stays the supported way to use `postgres.Store`") is no
longer true — out of date by this ADR itself, not silently ignored.
[`store.md`](store.md) and [`postgres-rfc.md`](postgres-rfc.md) were
updated to point at [`adapter.md`](adapter.md) as the current path;
`postgres-rfc.md` itself becomes a historical record of the original
design (most of its "Decided" points held up; the ones marked "open"
were resolved during the actual build, not revisited here).

## ADR-0003 — Outbox and Postgres

**Status:** Implemented — original deferral fulfilled (2026-11-15)

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

**Revision — 2026-11-15.** The historical deferral above was fulfilled by
Streams C1–C5 (PRs #29–#31). `outboxpostgres` stores events in the resource's
same-pool transaction; `comments-api` uses it and verifies real Pub/Sub delivery.
Shared events hooks accept an Adder; publishers/conditions remain in `outbox`.
The Mongo implementation remains separate. Postgres uses SKIP LOCKED, has no
cross-worker Key ordering, and implements dead_at/replay; Mongo retains its
leader lease and unbounded retry behavior. No generic storage interface or
distributed transaction was introduced. See [outbox](outbox.md) and the
[consolidated evidence](integration-review.md).

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

## Proposed evolution — 2026-11-16

ADRs 0007–0012 below are **Proposed**, without implied acceptance or
implementation. They extend the history above and map to Phases 6–11 of the
[roadmap](roadmap.md). The [DX and AI-readiness review](ai-readiness.md)
records the design comparison and audit corrections. Implementation requires a
consumer and validation criteria; code generation and RAG remain conditional on
evidence. New package names are illustrative.

## ADR-0007 — Canonical contracts and evidence-driven generation

**Status:** Proposed; contract direction recommended, generator deferred pending
consumer evidence.

**Context.** `declarative.Resource` compiles into `rest.Resource`;
`rest.Schema` already drives validation and OpenAPI. The Go path constructs these
types explicitly; it does not infer arbitrary domain structs. `Field.Check`,
`Resource.Validate`, hooks and programmatic filters cannot be serialized as plain
schema data.

**Problem.** An independent AI schema or bidirectional YAML/Go generator would
create drift. Dynamic `Doc` values may also make consumers repeat DTO definitions
and validation, but the size of that cost has not been measured.

**Alternatives considered.** Replace the core with Ent/GORM; infer everything
from reflection and tags; maintain independent schemas; generate all models now;
or reuse the current contract and measure duplication before generating code.

**Decision.** Propose the last option. `Resource`/`Schema` remain the canonical
runtime representation. Each resource has one chosen authoring source, YAML or
Go, without bidirectional synchronization. Optional AI descriptors live outside
the resource and reference its rules instead of duplicating them. Exporters
identify custom validation as additional server-side constraints, without
claiming full equivalence to JSON Schema.

First study one declarative and one programmatic resource, covering nested
objects, lists, nullability, required fields, defaults, uniqueness, PATCH and a
custom validator. Measure repeated definitions, maintenance effort and detected
drift. Only introduce generation when real consumers demonstrate recurring
savings. Any generator must be unidirectional and deterministic, pin its version,
keep generated files separate from manual hooks, and support a check-only mode.
Whether YAML should generate DTOs or another source should be used remains open.

**Consequences.** Discovery can proceed without generation. Input, PATCH and
output contracts remain distinct. IDs and dates follow the resource's codecs and
wire contract. REST schema does not acquire responsibility for DDL or data
migration.

**Risks.** Growing into a general compiler/ORM; losing Cerberus semantics such as
`AllowEmpty`; pretending callbacks are exportable; assuming a newly required
field is compatible with existing clients.

**Validation criteria.** Compare exported contracts with actual validation for
the cases above; diagnose unsupported constructs; avoid editing two schemas for
one field; preserve current examples. Keep generation deferred if the study does
not demonstrate value. Schema changes must document compatibility and migration,
including new required fields and type changes.

**Study evidence — 2026-11-16.** The [Phase 6 study](phase-6-contract-study.md)
found equal runtime schemas for the equivalent YAML/Go fixture and six
structural-schema/HTTP differences in 21 cases. Prioritize PATCH contract
corrections; keep generation deferred. This is evidence for review, not acceptance
of ADR-0007 or completion of its validation criteria.

## ADR-0008 — Optional tool catalog over existing operations

**Status:** Proposed; not implemented.

**Context.** `rest.API.Register` returns a `Service`, whose `Resource()` exposes
its definition. Services already implement operations and select storage through
`Adapter`. Hooks are suitable for write lifecycle effects; they do not provide
discovery, authentication or dispatch.

**Problem.** Exposing Go methods directly would duplicate CRUD semantics, leak
internal options and potentially enable operations disabled by the public
resource contract.

**Alternatives considered.** Automatically expose every resource; synthesize
HTTP requests; build separate AI CRUD; or explicitly register bounded operations
that call existing services.

**Decision.** Propose explicit, default-deny registration. Each tool declares a
stable name, contract version, description, permitted operation, input/output
fields, required policy, limits and effects. Derive argument structure from the
resource and add only operation-specific meaning. Start with one bounded read
for a real consumer. Domain actions reference existing application functions,
not workflows assembled through reflection.

The application supplies registered services to the catalog; no access to
private `rest.API` fields or database introspection is needed. Finalize the
catalog before serving. Reject naming collisions, operations incompatible with
configured resource methods and unsupported backend capabilities.
`SkipValidation`, `SkipConcurrency`, credentials, arbitrary SQL and raw adapter
access are never tool arguments. A package such as `aitools` is illustrative;
the integration does not belong entirely in `behaviors/ai`.

**Consequences.** The wrapper must map the relevant handler preparation:
backend-aware parsing, mandatory filters, pagination limits, context, rendering
and preconditions. Calling `Service` avoids a network hop but requires evidence
of parity. If parity cannot be maintained, reconsider an authenticated HTTP
client rather than maintaining inconsistent behavior.

**Risks.** Treating public routes as automatically authorized tools; serializing
internal fields from `Doc`; advertising Mongo query semantics on SQLite; mutating
metadata while serving concurrent requests.

**Validation criteria.** Unknown/hidden tools cannot execute; disabled methods
cannot be re-enabled by the catalog; responses match documented fields and
shape; invalid arguments fail before adapter access; validation, hooks and
preconditions match the existing operation. Prove this for each enabled backend
without claiming unsupported SQLite parity. Writes also require ADR-0010.

## ADR-0009 — Derived, bounded and authorized context discovery

**Status:** Proposed; not implemented.

**Context.** `rest/openapi.go` and `rest/swagger.go` describe registered resources
and operations. Their document-generation functions are internal; there is no
ready-made public generic MCP exporter.

**Problem.** Clients need operation formats and restrictions without receiving
business records, secrets or tools they cannot use.

**Alternatives considered.** Introspect the database; copy schemas into prompts;
expose the entire OpenAPI document; or project the registered contract according
to an explicit exposure policy.

**Decision.** Propose the authorized projection: permitted resources and
operations, arguments, limits, backend semantics and synthetic examples. Do not
include real documents by default. `DisableSwagger` and AI visibility are
independent; hiding Swagger neither protects a route nor authorizes publication
as a tool.

During implementation, compare an external `Resource`/`Schema` projection with a
small additive extraction of the existing OpenAPI serializer. Any new public
export API in `rest` must be neutral to AI, reviewed as public surface and preserve
the existing OpenAPI document. This is not a call to an already exported function.

Cache static metadata by contract version/hash. A user/tenant-filtered response
requires an authorization-scoped cache key. Never reuse a privileged view for
another principal. Discovery filtering and execution authorization are separate
checks.

**Consequences.** Custom validators remain server-side rules. Sensitive fields
require an exposure allowlist, not inference from names. Descriptions are reviewed
content without unrestricted record interpolation. Retrieved content is untrusted
data and cannot change executor policy.

**Risks.** Leakage through examples, descriptions, caches or errors; drift from
validation; treating an unchanged hash as proof of current authorization.

**Validation criteria.** Different scopes receive isolated views; descriptions
contain no secrets; exports are deterministic; incompatible changes update the
version/hash; accepted/rejected examples match the server; unrepresentable rules
are declared. Existing OpenAPI behavior remains unchanged.

## ADR-0010 — Safe execution with identity, policy and explicit effects

**Status:** Proposed; required before exposing tools to external clients or
enabling writes.

**Context.** HTTP authentication does not run automatically in `Service`
methods. `WriteOptions` includes internal check-bypass options. Existing dry-run
is a deployment mode and depends on correct transactional composition.

**Problem.** Reusing `Service` without a perimeter could permit cross-tenant
reads, writes without preconditions or external effects during supposed simulation.

**Alternatives considered.** Trust the model/prompt; trust MCP annotations; use
rollback as a universal sandbox; run arbitrary operations in subprocesses; or
provide a restricted, authorized operation wrapper.

**Decision.** Propose the restricted wrapper. Authenticate at the transport,
build a trusted principal, resolve the tool, authorize action/resource/tenant/
fields, validate and bound input, execute the existing operation, filter/render
the result and record the outcome. Identity and tenant come from verified
credentials, not model-chosen arguments. The application supplies explicit
policy; missing policy denies exposure.

Server-enforced tenant filters cover all reads and writes and cannot be replaced
by client filters. Projection or output filtering does not replace record-level
authorization. Cover `Get`, `List`, `Insert`, `Patch`, `Replace` and `Delete`,
including the resolved target and mutable fields. Forbid arbitrary queries and
validation/concurrency bypasses.

Every call has a deadline, payload/result limits and execution budget. Bound
lists even when REST pagination is disabled. Require concrete ETags where
applicable; do not use `*` to shortcut approval of sensitive changes. Record
actor, tool/version, resource, correlation ID, authorization decision and outcome,
redacting sensitive arguments.

Destructive/external-effect operations require approval according to application
policy. Bind approval evidence to principal, tool/version, canonical arguments,
resource/ETag, expiration and a single-use identifier. Changes invalidate it;
recheck policy and current state at execution to address TOCTOU. This is a future
product requirement, not an approval requirement for this documentation change.

Repeated mutations need durable application-provided idempotency: a key bound to
principal/operation/arguments, conflict on different content, atomic recording
with the mutation where possible, and a retention policy. Never blindly retry a
write after an ambiguous timeout. ETags alone do not make inserts or external
effects idempotent.

For dry-run, begin with reads and, if needed, validation preview without executing
side-effecting hooks. Reuse deployment-level rollback composition for Mongo and
Postgres only after inventorying hooks; do not toggle global state for one call.
Mongo's `WriteGuard` detects outside-transaction violations after persistence; it
does not prevent them. Validate Postgres transaction composition separately.
SQLite rejects dry-run. Refuse irreversible effects in this mode;
`RefuseInDryRun` is a precedent, not automatic coverage of arbitrary Go hooks.

**Consequences.** This is an allowlist of application operations, not a sandbox
for arbitrary code or AI-generated SQL. Record denied/failed attempts outside
aborted domain transactions; successful change events remain in the existing
transaction/outbox.

**Risks.** Authentication without domain authorization; log leakage; retries after
timeout; writes outside transactions; external pre-hook effects; approvals reused
with different arguments.

**Validation criteria.** Deny missing identity/policy, cross-tenant access and
forbidden fields; prove limits/cancellation; reject missing/stale ETags and
expired/altered approval; test concurrent idempotency keys; demonstrate no
persistent writes or external effects in advertised preview/dry-run modes. Never
report an unknown outcome as a confirmed rollback. Define application behavior
when mandatory audit recording fails.

## ADR-0011 — MCP as an optional catalog adapter

**Status:** Proposed direction; local SDK/stdio pilot implemented (see dated note).
Production client adoption and remote authorization remain open.

**Context.** MCP standardizes tool/resource discovery and invocation and continues
to evolve. The audited checkout contains no MCP server.

**Problem.** Embedding SDK/protocol concerns into the core would force unrelated
consumers to follow AI changes and could create a second execution policy.

**Alternatives considered.** Embed MCP in `rest`; replace HTTP; implement the
entire protocol manually; or isolate an adapter using an SDK compatible with the
chosen protocol version.

**Decision.** Propose an isolated adapter, illustratively `mcpbridge`, importing
the catalog/executor without reverse dependencies. An opt-in composition root
starts it. `tools/list` exposes permitted tools; every `tools/call` passes through
ADR-0010's executor. MCP resources, if needed, expose authorized discovery content,
not unrestricted tables/documents. Prompts and notifications stay outside the MVP
until demanded.

Select one real client and initial transport: stdio locally or authenticated HTTP
remotely. Pin protocol/SDK version, client compatibility and transport requirements
before implementation. Remote HTTP requires its own transport authorization,
token validation and session isolation; existing REST JWT authentication does not
prove MCP compliance. Stdio does not automatically establish an end-user identity.
Do not forward tokens to other services without audience validation and explicit
delegation design.

Annotations are descriptive hints, not authorization controls. Map protocol errors
and tool execution failures according to the chosen version without leaking stack
traces, SQL, DSNs or internal documents.

**Consequences.** Other clients can reuse the same catalog. Opt-in deployments
configure connection limits, cancellation and credentials. REST clients require
no AI configuration. `platform/*` never imports `rest`; neither `rest` nor `app`
imports MCP or model SDKs. If dependency-graph isolation from the root module is
required, evaluate a separate submodule; separate packages alone do not promise it.

**Risks.** Incompatible negotiation; shared sessions between users; trusting
annotations; unfiltered catalogs; duplicate policies.

**Validation criteria.** A real client discovers and invokes the bounded read;
direct calls to hidden tools are denied; users are isolated; disconnect/deadline
cancels work; malformed input and unsupported versions fail predictably; all calls
use the same executor. Add mutations only after ADR-0010's criteria pass.

Primary references: [MCP tools, 2026-07-28 specification](https://github.com/modelcontextprotocol/modelcontextprotocol/blob/main/docs/specification/2026-07-28/server/tools.mdx),
[MCP authorization, 2026-07-28 specification](https://github.com/modelcontextprotocol/modelcontextprotocol/blob/main/docs/specification/2026-07-28/basic/authorization/index.mdx),
and [MCP tools, 2025-06-18 version, including annotation trust](https://github.com/modelcontextprotocol/modelcontextprotocol/blob/main/docs/specification/2025-06-18/server/tools.mdx).
Implementation must verify support in the selected SDK/client; no compatibility
matrix has been validated in this documentation round.

**Implementation note — 2026-11-16.** The [local MCP pilot](mcp-pilot.md)
selects official SDK v1.8.0 and protocol 2025-11-25, with a separate internal
adapter, real SDK memory/stdio clients and per-operation authorization/audit.
Production client adoption, MCP HTTP/OAuth and multi-user isolation remain open;
this evidence does not mark the entire ADR scope implemented.

## ADR-0012 — Consumer-driven retrieval synchronized through outbox

**Status:** Proposed for general application integration; an optional static
[documentation pilot](knowledge-pilot.md) now implements pgvector ingestion,
manifest ACLs and MCP retrieval. Real-model quality evaluation remains open.

**Context.** `platform/search` writes to Elasticsearch;
`behaviors/searchsync` synchronizes after commit on a best-effort basis. Mongo and
Postgres have transactional outbox implementations and shared hooks. No embeddings,
vector search or RAG consumer was identified in the audited source.

**Problem.** A universal retrieval abstraction would hide authorization,
consistency, cost and quality choices before a corpus is known. External embedding
calls inside synchronous writes increase latency and cannot share the database's
atomic commit.

**Alternatives considered.** Embed synchronously in hooks; turn `searchsync` into
a generic durable pipeline; create a universal vector store; select pgvector or
Elasticsearch for convenience; or defer implementation with a minimal direction
for a concrete consumer.

**Decision.** Propose deferral. Revisit when a team supplies a permitted corpus,
real queries, freshness target, tenant/field policy, budget and lexical-search
baseline. Add vectors only if evaluation demonstrates useful gains. No provider,
embedding dimension, database extension or vector database is selected here.

For the first consumer, keep workers and retrieval optional above infrastructure
adapters. Compare lexical, vector and hybrid retrieval on the same corpus. Build
one concrete implementation before generalizing; a real second implementation
triggers extraction of shared contracts.

When triggered, write the document and minimal reindex event in the same local
transaction through `behaviors/events` and `outbox` or `outboxpostgres`. A worker
consumes at least once, rereads the authorized version, selects permitted fields,
chunks and embeds outside that transaction. Stable chunk IDs and document/model
versions support idempotence; keys include tenant/resource. Handle duplicates,
concurrent updates and out-of-order delivery with version checks/tombstones. The
Postgres outbox does not guarantee per-key ordering.

Do not rely exclusively on `ConditionalHooks` for deletion: the observed hooks
cover insert/update/replace, while `events.Hooks` also covers delete. Deletion and
ACL changes require suitable explicit events. Stale events must not resurrect
deleted records; backfill shares version rules and reconciles live updates.

Version embedding model/dimension, chunker, indexed fields and index contract.
Reindex into a new index and switch deliberately when these change. Bound retries
and backoff; use quarantine/DLQ according to actual backend support. Mongo does
not already have the same DLQ as Postgres. Index failures never undo an already
committed source mutation.

Authorize before retrieval, apply tenant/ACL restrictions to search and recheck
source documents before returning context. The index is not an authorization
authority. Bound top-k, context size and token budget. Propagate deletion and
revocation; scope caches by authorization and version. Results carry document
references, versions and excerpts for citations. Retrieved content is untrusted
data and cannot change policy, approvals or available tools. Any generation step
belongs to the application, with explicit unsupported-answer/abstention behavior.

**Consequences.** Eventual consistency is documented and measured, not
exactly-once. SQLite is excluded from the transactional pipeline until it supports
outbox. Catalog/MCP adoption does not require RAG.

**Risks.** Cross-tenant leakage; sensitive data sent to a provider; reindex cost;
out-of-order events; deleted content remaining searchable; plausible unsupported
answers; silent quality regressions.

**Validation criteria.** Define Recall@k/nDCG, citation quality/abstention, p95
latency, query/indexing cost and freshness thresholds before implementation.
Evaluate representative queries with documented judgments against the lexical
baseline. Require no unauthorized results in the isolation suite and immediate
source-based denial of deleted/revoked records even with a stale index. Test
duplicate/reordered delivery, replay, crash after indexing and resumed backfill.
Keep implementation deferred without measurable gains or a viable budget.

**ADR-0012 implementation note — 2026-11-16.** The local documentation consumer
now has a [versioned Postgres source and outbox](knowledge-sync.md), source ACL/version
checks on retrieval, tombstones and replay-safe indexing. General application
producer wiring and production operations remain explicit integration work.
