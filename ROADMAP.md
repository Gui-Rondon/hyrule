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
run are future work driven by real users. YAML resources already support
`backend: sqlite` within the same experimental limits; see the
[SQLite reference](website/docs/reference/sqlite.md). No SQLite production
parity or v1 release date is promised.

## Proposed DX and AI evolution

[ADRs 0007–0012](website/docs/reference/architecture-decisions.md) and
[Phases 6–11](website/docs/reference/roadmap.md) extend the framework/platform
plan with contracts/DX, an authorized tool catalog, discovery, optional MCP and
consumer-driven retrieval. The standalone example now contains a local `tags.lookup` executor prototype;
external authentication/transport and the broader proposals remain open. Code
generation requires consumer evidence. An optional [pgvector documentation pilot](website/docs/reference/knowledge-pilot.md) now provides manifest ingestion, subject ACLs and MCP search/fetch; local model evaluation and a versioned Postgres source/outbox path are implemented; broader quality acceptance and production producer wiring remain open. They are independent of the v1 gates above.
See the [review and audit corrections](website/docs/reference/ai-readiness.md).
