# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/) with a
pre-1.0 convention: the major version stays `0`, and a breaking change bumps
the minor version instead.

## [Unreleased]

### Changed

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
