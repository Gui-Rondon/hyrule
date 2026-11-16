# Roadmap toward v1

Current capabilities and future criteria are separate. See [maturity/support](COMPATIBILITY.md)
and the [historical architecture roadmap](website/docs/reference/roadmap.md).

| Priority | Deliverable | Done when |
|---|---|---|
| P0 | Safe upgrades and contribution | Compatibility/support policies, changelog, review process and contribution templates published |
| P0 | First API without infrastructure | Released CLI generates a SQLite API, two resources run and data survives restart without Docker |
| P0 | Required CI | Go 1.26/1.27 tests, adapter integrations and 95% framework gate pass on PRs |
| P1 | Public package docs | Compiling examples and linked Overview are visible in the next indexed release |
| P1 | Router choice | Gin, Chi and Echo compose the same handler, preserving CRUD, middleware and Swagger under a prefix |
| P1 | Release and supply chain | Maintainer-dispatched release creates tag, Linux/Windows binaries, checksums and verified SLSA attestations; module requested from proxy |
| P1 | GitHub hardening | Private reports, protected main, CodeQL and scheduled Scorecard active after workflow checks exist |
| v1 gate | Mature contracts | Inventory reviewed, known breaks resolved, two compatible minors, migration guide and reproducible examples |

SQLite remains experimental. Advanced SQLite queries, transactional outbox, dry
run and YAML support are future work driven by real users. No SQLite production
parity or v1 release date is promised.
