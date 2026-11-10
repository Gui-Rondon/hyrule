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

### Added

- `examples/standalone-tags-api`: Fase 2's proof that an external
  repository can depend on Hyrule as its own Go module (own `go.mod`,
  `replace` directive until a version is tagged), not just as a package
  inside this monorepo. A YAML-declared resource, one Go hook
  (`behaviors/audit`), and a `testkit`-based test.
- Phase 0 architectural audit of the Framework/Platform evolution: five ADRs
  ([`architecture-decisions.md`](website/docs/reference/architecture-decisions.md))
  and a phased [roadmap](website/docs/reference/roadmap.md).
