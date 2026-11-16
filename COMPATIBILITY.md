# Compatibility and support

Hyrule follows Semantic Versioning. The compatibility contract includes exported
Go APIs, HTTP status/body/header behavior, query semantics, configuration keys,
CLI syntax and generated layouts. Backend differences are documented rather than
hidden behind a promise of universal parity.

Before v1, breaking changes increment the minor version; patches remain compatible.
After v1, breaking changes require a new major and the corresponding Go module
path for v2+. Every observable change needs a CHANGELOG entry; incompatible changes
need a migration example. Deprecations remain available for at least one minor
release before removal in an appropriate breaking release. Urgent security fixes
may shorten that notice, with impact and migration instructions disclosed.

## Supported releases

- Before v1: fixes target the latest minor, on its newest patch.
- From v1: the two latest minors of the current major receive fixes.
- After a new major: the previous major's final minor receives security fixes for
  six months. Older releases receive upgrade guidance, not indefinite backports.
- Go 1.26 and 1.27 are supported, tested with automatic toolchain switching disabled.
  Changes to the minimum Go version require release notes and a compatibility review.

## Maturity today

| Surface | Status and limits |
|---|---|
| `rest` Go/HTTP resource contract, Mongo bootstrap | Established pre-v1; protected by integration tests, still subject to minor-version breaks |
| Postgres adapter/bootstrap/outbox | Available pre-v1; backend differences documented in the backend guide |
| CLI and scaffold layout | Evolving; this release changes the default to a single SQLite API |
| SQLite | Experimental local experimentation; scalar queries, no outbox/dry run/soft delete |
| Declarative YAML and testkit database provisioning | Mongo-specific |
| HTTP router composition | `http.Handler` embedding; Hyrule retains its internal routes |

No pre-v1 surface is represented as a stable v1 API merely because it is exported.

## v1.0.0 readiness

The maintainer must review the inventory of exported contracts and resolve known
breaking changes before nominating v1. Require two consecutive minor releases
without incompatible changes to candidate contracts, a green supported-Go matrix,
real adapter integrations, reproducible external examples, and migration notes
from the preceding minor. Feature count is not a release gate. Experimental
features remain explicitly identified until they meet the same maturity criteria.
