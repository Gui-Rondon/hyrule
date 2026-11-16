---
sidebar_position: 21
---
# Stability, releases and support

Hyrule is pre-v1. Exported APIs are available to consumers, not a blanket stability
promise. The [compatibility policy](https://github.com/Gui-Rondon/hyrule/blob/main/COMPATIBILITY.md)
covers Go, HTTP, CLI and configuration contracts, maturity and support windows.
Before v1, breaking changes require a minor bump; after v1, a major bump.
Deprecations stay for at least one minor except documented urgent security fixes.

Before v1, support covers the latest minor's newest patch. From v1, the latest two
minors of the current major receive fixes; the preceding major's final minor gets
six months of security fixes after a new major. Go 1.26 and 1.27 are tested.

v1 requires an audited public-contract inventory, no pending known breaks, two
consecutive compatible minor releases, green integration/Go checks and reproducible
external examples with migration notes. SQLite stays experimental until mature.

Release publication is manual by the maintainer, with Linux/Windows amd64/arm64
binaries, checksums and signed GitHub/SLSA provenance. This is not certification
of a SLSA level. Proxy index requests are automated; pkg.go.dev/deps.dev may lag.
See [release operations](https://github.com/Gui-Rondon/hyrule/blob/main/docs/releasing.md).

The package docs include compiling examples. Stable-version status appears at v1,
not by editing metadata. Attestations of release files do not automatically attest
the Go proxy module zip; deps.dev's slsaProvenances field is npm-only.

For help contributing, see [CONTRIBUTING](https://github.com/Gui-Rondon/hyrule/blob/main/CONTRIBUTING.md).
Security reports use private GitHub reporting, with an initial response target of
seven calendar days. The current maintainer reviews changes; independent required
approvals await additional maintainers. GitHub branch-protection activation follows
successful publication of the required checks, not the presence of YAML alone.
