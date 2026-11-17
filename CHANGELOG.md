# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/) with a
pre-1.0 convention: the major version stays `0`, and a breaking change bumps
the minor version instead.

## [Unreleased]

### Fixed

- Align homepage card links at the bottom of each card so Guide, Reference and
  Blog calls to action share the same baseline.

- Reconcile ADR-0007–0012 and EN/PT audit/pilot documentation with the implemented
  PR #70 increments, separating historical audit context, current local evidence,
  formal decision status and remaining production scope.

- Validate and read each knowledge document through a single descriptor, removing
  the reopen window during ingestion. Share regular-file and bounded-read checks
  across the knowledge and tags MCP/curator commands.

- Preserve nested required fields in OpenAPI PATCH request schemas, including
  object items in lists, while allowing omitted root fields. This aligns the
  published contract with existing validation; runtime behavior and response
  schemas remain unchanged.

### Added

- Add a concrete Postgres article producer using Hyrule Service pre-write hooks
  to commit resource/source/outbox together. Include a curator CLI with distinct
  JWT write permission, ETag enforcement, private-field projection and corpus
  isolation; exercise the producer through authenticated MCP retrieval.

- Add versioned knowledge sources with transactional Postgres outbox updates,
  authoritative ACL/version checks during retrieval, tombstones and replay-safe
  indexing. Add `knowledge-mcp enqueue|worker`, optimistic source writes and tests
  for revocation before indexing, rollback, concurrent updates, DLQ and replay.

- Support separate document/query embedding prompts and hash their storage
  contract. Add opt-in cached ONNX evaluation with bilingual questions, a lexical
  baseline and recorded local EmbeddingGemma results (10/10 Hit@5).

- Add optional documentation ingestion and MCP search/fetch with pgvector, explicit
  corpus/subject ACLs, Ollama embeddings, independent audit and a storage interface
  for a future Qdrant implementation. Include real pgvector and stdio tests.

- Add opt-in `cmd/tags-mcp` with explicit environment/file configuration,
  read-only existing SQLite data, separate audit and per-operation token-file
  refresh. Test the compiled executable with an SDK client, stdout discipline,
  startup errors and EOF/interrupt shutdown; document EN/PT client setup.

- Add an optional internal MCP adapter to the standalone example using official
  Go SDK v1.8.0 and protocol 2025-11-25. Reuse authorized discovery and execution,
  bound stdio frames and verify real SDK clients over memory/stdio, denial, audit
  and cancellation. Keep the SDK outside the framework module and default startup.

- Publish authorized input/output JSON Schemas, executor limits, explicit
  server-only constraints and deterministic contract hashes in tags-pilot
  discovery. Derive field types from the registered resource and verify schema
  parity with actual parsing and lookup results; keep generic catalog/MCP deferred.

- Add opt-in loopback HTTP transport for the tags pilot: JWT-protected GET
  discovery and POST lookup, mandatory audit, strict bounded requests, sanitized
  errors and explicit server timeouts. Verify real HTTP/RSA/SQLite integration
  and slow-body timeout; keep default startup and remote deployment unchanged.

- Provision the local tags pilot from an explicit public-key PEM and persist
  sanitized audit records in a separate SQLite file with WAL/FULL synchronization;
  fail closed on audit errors and verify persistence/reopening and concurrency.

- Integrate RS256 JWT identity and exact scope-based policy into the local tags
  pilot, requiring pinned keys, issuer/audience, expiration and subject. Test real
  signatures, rejected credentials and resource/action binding without changing
  REST authentication or exposing an endpoint.

- Add the application-local `tags.lookup` executor under the standalone example
  with injected identity/policy, mandatory audit, bounded strict JSON input,
  allowlisted output and SQLite-backed authorization/failure/concurrency tests.
  No external endpoint, identity-provider integration or framework API is added.

- Define the proposed `tags.lookup` read pilot, its explicit access policy and
  bounded input/output, with a reproducible SQLite query rehearsal preceding the
  local executor above. Authentication integration and MCP transport remain open.

- Mark full-write and PATCH OpenAPI input objects with
  `x-hyrule-custom-validation: true` when `Resource.Validate` is configured,
  distinguishing document-wide server validation from per-field `Field.Check`.
  This annotation does not execute callbacks or change runtime validation.

- Record the Phase 6 contract study in English and Brazilian Portuguese: 21
  SQLite-backed cases, six structural-schema/HTTP differences, a reproduction
  harness, and evidence for keeping code generation deferred. The PATCH exporter
  correction is recorded above; ADR acceptance remains open.

- Document proposed ADR-0007–0012 and framework/platform Phases 6–11 in
  English and Brazilian Portuguese, with a DX comparison and audit corrections.
  Tool execution, discovery, MCP, code generation and RAG remain unimplemented
  proposals; this documentation adds no runtime capability.

- `resources.yaml`'s declarative resources now take an optional `backend:`
  field (`mongo`, the default, `postgres` or `sqlite`), dispatched through a
  small per-backend `declarative.Backend`/`Registry` seam; `hyrule-serve`
  picks its own `BACKEND` env var accordingly. Existing files with no
  `backend:` field keep working unchanged.

### Security

- Resolve the latest Go security patches in CI while preserving the 1.26/1.27
  matrix; document the local-toolchain standard-library advisories.
- Reject PostgreSQL pool sizes that overflow int32 before opening a connection.
- Keep extreme pagination values from overflowing into negative page numbers.
- Update vulnerable website serialization, worker-pool and UUID dependencies;
  use a documented, private deepmerge security fork with prototype-poisoning
  regression tests until a compatible upstream fix is available.
- Audit the complete website/build dependency tree in the existing docs jobs
  and on scheduled vulnerability scans; keep Go scanning unchanged.
- Pin build/runtime and integration images by digest, with Dockerfile updates
  through Dependabot and documented manual checks for Compose-only images.
- Add query, SQL compilation, validation and merge-patch fuzz targets. Their
  seeds run in normal tests; bounded campaigns run every 15 days on Go 1.27
  (four sequential 5-minute targets, two workers, 30-minute job timeout).
- Include both hyrule and hyrule-serve in the Linux/Windows release matrix,
  with checksums and existing artifact provenance covering all eight binaries.


### CI maintenance

- Wait for an actual Pub/Sub emulator HTTP response before Compose reports it
  ready. Bound the CI readiness probe and retry connection resets (curl exit 56)
  as well as refused connections, preventing a startup race before integration tests.
  Use curl from the pinned emulator image and emit healthcheck diagnostics on failure;
  the current image does not provide a system python3 executable.

- Refresh external-example module graphs before integration and vulnerability
  checks, since their local replace follows root dependency updates.
- Run the Go 1.27 integration suite once with race and coverage instead of three
  times; preserve full Go 1.26 and native Windows checks. Coverage failures now
  retain their test output in .local/coverage-test.log.
- Make the relay lease handoff test independent of a 300ms database timing window,
  preserving real Mongo exclusivity/expiry checks under race and coverage.
- Cancel superseded PR workflow runs and group Dependabot version updates by
  ecosystem to reduce redundant checks. Existing dependency PRs are unchanged.


### Adoption, compatibility and supply chain

- **Breaking (next minor):** `hyrule new` now defaults to a single SQLite API
  runnable with `go run .`; use `--layout=platform` for the previous Mongo
  multi-service scaffold. Existing manifest-free platform projects still work.
  Generated projects pin the installed CLI release; development CLI builds require
  `--framework-version`. `service create` resolves dependencies for the single layout.
- Experimental `platform/sqlite`, `restsqlite` and `app.BackendSQLite`: persistent
  local CRUD, transactions, real unique indexes, UUID/version ETags, RFC 7396
  merge patch and scalar queries. No outbox, dry run, soft delete or YAML support.
- Upgrade klauspost/compress to v1.20.2 for GO-2026-6865.
- Support Go 1.26/1.27; PR CI runs real adapter integrations, race/vet checks,
  generated/external examples, EN/PT docs and the existing 95% runtime gate.
- Compatibility/support and v1 maturity policies, contribution/review process,
  security reporting (seven-day initial response), issue/PR templates and roadmap.
- Compiling pkg.go.dev examples and clarified app documentation; tested Gin/Chi/Echo
  handler composition in a standalone example, without core router dependencies.
- Maintainer-dispatched Linux/Windows CLI releases with checksums, verified GitHub
  SLSA provenance, immutable version checks and bounded Go proxy index requests.
  No release/tag is published by this PR; pkg.go.dev/deps.dev ingest asynchronously.
- Pinned, least-privilege Actions; CodeQL, vulnerability scans, Dependabot and
  scheduled Scorecard. Branch protection/private reporting activation is tracked
  separately in docs/releasing.md; workflow files alone do not activate settings.


### Added

- Automatic OpenAPI 3.1 and embedded Swagger UI at `/api/openapi.json` and
  `/api/docs` (`/swagger` redirects). Enabled by default for Go/YAML resources
  on both backends; `DisableSwagger`/`disableSwagger` hides individual resources
  without changing their API routes. Documentation endpoints are public and reserved.

### Changed

- Require 95% runtime framework coverage in `make cover` and CI, with all normal
  tests still executing against both real backends. `make cover-all` retains the
  previous aggregate scope (examples/tools included) and 85% gate separately.
- Preserve context causes when read timeouts become HTTP 503, so wildcard PATCH
  recognizes retry budget exhaustion during a real database read. HTTP status
  and message remain unchanged; use `errors.As` for the classified `*rest.Error`.

- **Breaking:** per [ADR-0001](website/docs/reference/architecture-decisions.md#adr-0001--public-surface-of-the-framework),
  `app`, `behaviors/*`, `declarative`, `outbox`, `platform/*`, `rest`, and
  `testkit` moved from `internal/` to the module root. Import paths change
  from `github.com/Gui-Rondon/hyrule/internal/<pkg>` to
  `github.com/Gui-Rondon/hyrule/<pkg>`. `internal/domain` is the only package
  that stays internal. Behavior is unchanged; this is purely a move
  (confirmed by an external scratch module importing `rest`, `app`,
  `declarative`, and `platform/mongodb` without touching `internal/`).
  `rest.Store` itself stays Mongo-shaped per
  [ADR-0002](website/docs/reference/architecture-decisions.md#adr-0002--portability-of-the-reststore-contract) —
  no change to its contract in this round.
- **Breaking** (pre-1.0, per [ADR-0005](website/docs/reference/architecture-decisions.md#adr-0005--versioning-and-compatibility-pre-10)),
  as part of the Postgres portability work below: `rest.WriteOptions.IfMatch`
  is now the `IfMatch` type, not a plain `string`; the `lookup` parameter of
  `Service.Get`/`Find`/`Patch`/`Replace`/`Delete` and `ListQuery.Filter`/
  `Sort`/`Projection` are now `any`, not `bson.M`/`bson.D` — a `bson.M`
  literal still satisfies `any` without caller changes, confirmed against
  every existing caller in this repo.
- Per [ADR-0002's revision](website/docs/reference/architecture-decisions.md#adr-0002--portability-of-the-reststore-contract):
  a resource picks a backend by setting `Resource.Adapter`
  (`rest.Adapter`, a new interface a `Resource` writes through for
  parsing/combining `where`/`sort`/`projection`, CRUD, id generation,
  merge-patch, uniqueness and error classification) instead of implementing
  a new `Store`. `Resource.Store` still works exactly as before — `rest`
  wraps it in a `mongoAdapter` automatically when `Adapter` is unset. See
  [`adapter.md`](website/docs/reference/adapter.md).

### Fixed

- Stop and join Mongo background workers before disconnecting on every Run
  return path, matching the Postgres lifecycle and allowing worker cleanup to
  finish against the database.
- Reject unknown `app.Spec.Backend` values before bootstrap selects a database;
  zero-value Mongo and explicit `BackendPostgres` keep their existing behavior.
- Refresh the standalone example's module graph/checksums for the shared
  Mongo/Postgres bootstrap. Add real dual-backend HTTP contract and pool
  configuration coverage.
- Consolidate EN/PT backend setup, transactional outbox, dry run, opt-in adoption
  and gate evidence. Document public API migration notes and the two e2e
  add/add conflicts that still require resolution before a future main merge.

- `cmd/hyrule/templates/new/Makefile.tmpl`'s `build` target now fails
  with a clear message when `SERVICE` is empty, instead of running a
  broken `go build`. Also creates `.local/` explicitly before writing
  to it (already implicit in Go ≥1.21's `go build -o`, kept explicit
  for clarity and to not depend on that behavior).
- Added `cmd/hyrule/e2e_test.go`'s `TestScaffoldBuilds`: the first
  automated test that actually runs `hyrule new` → `hyrule service
  create` → `go mod tidy`/`go vet` → `make build` on a generated
  project and checks for a real binary, rather than only checking
  which files got written. The generated project's own `go test` runs
  too when `MONGO_TEST_URI` is set, skipped otherwise (no Docker
  required for the rest of the test).

### Added

- `restpostgres`: `rest.Adapter` implemented for Postgres
  (`postgresAdapter`/`NewAdapter`), over a `jsonb` document column plus
  relational metadata columns (`id`/`created_at`/`updated_at`/`version`/
  `deleted`) — RFC 7396 merge-patch (diverging from Mongo's deep merge,
  on purpose), optimistic concurrency via the `version` column with a
  real retry budget under `If-Match: *`, SQLSTATE-based error
  classification. `platform/postgres` grew from a `Store`-shaped scaffold
  into the DSL compiler this needs: `CompileFilter`
  (`$eq/$ne/$gt/$gte/$lt/$lte/$in/$nin/$exists/$has/$hasSome/$hasEvery/
  $not/$and/$or`), `SchemaSpec`/`Kind`, `Transactor`, `EnsureFunctions`
  (the `hyrule_safe_*` SQL functions `KindDateTime` filtering/sorting/
  indexing depends on), and `EnsureIndexes`/`IndexSpec` (declarative
  indexes, narrower than Mongo's — no TTL/text/hashed/2dsphere).
  `examples/blog-apis/services/comments-api` is the first resource
  served end to end over HTTP through this path (there's no `app.Main`
  support for Postgres yet, so it wires `platform/server`+
  `platform/postgres`+`restpostgres` directly in its own `main()`). See
  [`adapter.md`](website/docs/reference/adapter.md); supersedes
  [`postgres-rfc.md`](website/docs/reference/postgres-rfc.md), kept as
  the historical design record.
- Exact numeric precision for a JSON request body: `rest/http.go`'s
  `readBody` decodes with `json.Decoder.UseNumber()` instead of letting
  every number become a lossy `float64`, so an integer beyond 2^53 in a
  declared `TypeInteger`/`TypeNumber` field round-trips exactly on both
  backends. Scientific notation (`1e3`) and a declared `TypeInteger`
  value outside `int64`'s exact range are now rejected (422), instead of
  silently rounded. An undeclared field's number outside `int64` is
  still accepted on both backends, but now visibly diverges by design —
  exact on Postgres (`jsonb`), rounded through `float64` on Mongo (BSON
  has no arbitrary-precision integer type) — rather than being an
  unexamined edge case.
- A "Platform" section under [Learn](website/docs/learn/intro.md):
  `website/docs/platform/{intro,creating-a-project,creating-services,
  local-development,ci-and-deploy}.md`. Covers exactly `hyrule
  new`/`hyrule service create` and the Fase 4 `ci/affected`/`deploy.sh`
  pattern — every unbuilt command (`dev`, `build --affected`, `deploy`)
  is named as unbuilt, not omitted. English only, matching the rest of
  Learn.
- `.github/workflows/blog-apis-affected.yml` (ADR-0006): runs `ci/affected`
  for real on push/PR, builds only the services it reports, and calls each
  one's new `deploy/<service>/deploy.sh` (a stub — the configurable seam
  for a real provider's deploy command). Lives in `examples/blog-apis`, not
  a Hyrule-shipped capability; no provider abstraction and no per-service
  versioning scheme, since neither has a real consumer yet.
- `cmd/hyrule`: the Platform CLI (ADR-0004). `hyrule new <project>`
  scaffolds a multi-service project (`go.mod`, `Makefile`,
  `docker-compose.yml`); `hyrule service create <name>` scaffolds a
  service in it (`app.Main` + one resource + a `testkit` test).
  Scaffolding only — `dev`, `build --affected`, and `deploy` are not
  implemented, and `ci/affected`/the Makefile pattern aren't wrapped as
  subcommands yet; that's deferred on purpose, not missing by oversight.
- `examples/standalone-tags-api`: Fase 2's proof that an external
  repository can depend on Hyrule as its own Go module (own `go.mod`,
  `replace` directive until a version is tagged), not just as a package
  inside this monorepo. A YAML-declared resource, one Go hook
  (`behaviors/audit`), and a `testkit`-based test.
- Phase 0 architectural audit of the Framework/Platform evolution: six ADRs
  ([`architecture-decisions.md`](website/docs/reference/architecture-decisions.md),
  each now with a **Status**/**Implemented in** line)
  and a phased [roadmap](website/docs/reference/roadmap.md).
