# Security maintenance and validation

Scorecard is a set of automated signals, not security certification. Compare the
report date and analyzed commit with main before acting on an individual score.
Do not suppress advisories, fabricate reviews or change public APIs to improve it.

## Website and build dependencies

The November 16, 2026 snapshot reported seven advisories in the website lockfile:

| Package | Original | Remediation |
| --- | --- | --- |
| serialize-javascript | 6.0.2 | 7.1.4; covers GHSA-5c6j-r48x-rmvq, GHSA-m5mm-p8h7-m29v and GHSA-qj8w-gfj5-8c6v |
| tinypool | 1.1.1 | 2.2.0; covers GHSA-5gmw-xhrv-c9v3 and GHSA-85c8-ppgw-ccpr |
| uuid (sockjs) | 8.3.2 | 11.1.1; covers GHSA-w5hq-g745-h8pq while keeping CommonJS v4 compatibility |
| deepmerge | 4.3.1 | Private local fork fixing GHSA-gcqx-w48m-w2h4; see `website/vendor/deepmerge/README.md` |

Docusaurus 3.10.2 still declares the older transitives. Overrides are temporary,
tested compatibility decisions, not claims that upstream constraints changed.
The local fork retains upstream source/license and documents the exact patch;
its separate package identity must not be mistaken for an upstream fixed release.
An audit with no known advisories does not independently validate private code.
The exploit regressions and normal EN/PT production build validate this tree.
`website/.npmrc` installs the local package as a regular copy; after changing its
source, run `npm ci` so tests and builds use the updated copy.

The affected consumers are build tooling (SVGR, webpack copy/minification and
Docusaurus SSG) and the local dev server (sockjs). Sockjs calls UUID v4 without
an output buffer; the reported UUID defect concerns v3/v5/v6 with output buffers.
Docusaurus calls `pool.run(task)` without a second options object, so the
inherited `run()` options exploit is not demonstrated on that call path.
Neither fact justifies retaining vulnerable packages: build inputs include PR
content, and future callers may differ. Inventory and demonstrated reachability
are distinct. No claim is made that all npm code is shipped to the static site.

```sh
cd website
npm ci
npm run test:security
npm audit
npm explain deepmerge serialize-javascript tinypool uuid
npm run build
```

PR/push auditing runs in the existing documentation job. The scheduled/manual
Vulnerabilities run audits the lockfile without installing or running scripts.
Keep the full graph in scope, including build dependencies. Review new failures;
do not add audit exclusions or run `npm audit fix --force` blindly. At each
dependency update, check for compatible upstream fixes and remove overrides only
after regression tests and the EN/PT build pass.

## Go toolchain security

Use the latest security patch on each supported Go line. The language baseline
in go.mod remains Go 1.26; this does not change the framework's public API.
At validation time, the local default Go 1.27.2 exposed two standard-library
advisories: [GO-2026-6859](https://pkg.go.dev/vuln/GO-2026-6859) (fixed in
1.26.11/1.27.4) and [GO-2026-6809](https://pkg.go.dev/vuln/GO-2026-6809) (fixed in
1.26.10/1.27.3). Rebuilding is required to fix a compiled binary; changing only
go.mod or suppressing scanner output does not fix it. Release validation and
scans use the latest patch, with setup-go's check-latest enabled. Local validation
uses explicit patched toolchains without replacing the developer's global Go.
CodeQL and the affected-service example also use the latest supported Go 1.27
patch instead of installing the exact older language baseline from go.mod.

## CodeQL triage

The analyzed main had six open alerts. Workflow success does not imply an empty
alert list. Changes here do not dismiss GitHub alerts or claim a fresh CodeQL run.

| Alerts | Evidence and decision |
| --- | --- |
| #1/#2, `go/incorrect-integer-conversion`, `platform/postgres/connect.go` | Positive pool settings could overflow int32. Reject values above MaxInt32 before casting/dialing; regression checks cover both configuration keys. Verify closure in the next CodeQL analysis. |
| #3/#4, `go/unsafe-quoting`, `rest/schema.go` | These concatenate display messages, not SQL literals. HTTP validation errors go through WriteError/WriteJSON with application/json and json.Encoder; quotes are encoded as data. `TestValidationMessagesRemainJSONData` verifies the sink. Candidate false positives for this path, not an administrative dismissal; clients must encode messages when embedding them in HTML. |
| #5/#6, `go/sql-injection`, `platform/mongodb/store.go` | The framework intentionally accepts a BSON query DSL. Collection GET parses it through parseListQueryFor → mongoAdapter.ParseWhere → recursive checkOperators before Find/CountDocuments. Defaults reject $where/$function/$accumulator. Item IDs and additional-lookup values are typed scalar filter values. Extend TestBadQueries to exercise nested denied operators; the fuzzer independently checks accepted inputs. Custom routes/direct Store callers and resources overriding the denylist remain trusted application code and need caller review. Do not sanitize Store globally or claim all custom filters are safe. |

References: https://github.com/Gui-Rondon/hyrule/security/code-scanning
and https://github.com/ossf/scorecard/tree/61e84743356275f6ffca23b1b2f2cb945e19eb96

## Fuzz campaigns

Seeds execute in ordinary Go tests on the existing Go 1.26/1.27 matrix. Campaigns
run in one Linux/Go 1.27 job, without databases or emulators, every 15 days from
2026-11-16 UTC. GitHub cron has no exact 15-day interval across months, so a daily
gate selects eligible days; noncampaign days do not check out or compile code.
Scheduling is best effort; a manual dispatch can compensate for a missed run.

The fuzzing runtime budget is four sequential targets, five minutes each, with `-parallel=2`;
the entire job has a 30-minute timeout including setup/compilation. Later targets
still run after an earlier failure without turning the job green. Failure corpora
are retained for 30 days. Reproduce crashes and commit minimized inputs as
regression seeds. Review cost and discoveries after three campaigns.

```sh
go test ./rest -run='^$' -fuzz='^FuzzParseListQuery$' -fuzztime=5m -parallel=2 -timeout=6m
go test ./platform/postgres -run='^$' -fuzz='^FuzzCompileFilter$' -fuzztime=5m -parallel=2 -timeout=6m
go test ./rest -run='^$' -fuzz='^FuzzValidateDict$' -fuzztime=5m -parallel=2 -timeout=6m
go test ./restpostgres -run='^$' -fuzz='^FuzzMergePatch$' -fuzztime=5m -parallel=2 -timeout=6m
```

Assertions cover denied operators and pagination, bound SQL values/placeholders,
full versus partial validation, and RFC 7396 null/array/idempotence/no-aliasing
properties. Mongo deep merge has a different contract; do not apply RFC 7396
expectations to it. Inputs are bounded to 4 KiB per case to keep runtime useful.

## Images and release provenance

Dockerfiles and Compose files pin remote images to registry-verified digests.
Use multiarchitecture index digests when images support multiple targets. Docker
Dependabot updates Dockerfiles; manually review Compose-only images weekly and
after advisories. Re-resolve with `docker buildx imagetools inspect IMAGE:TAG`,
review the publisher/version, then run the existing integration gates. Pinning
does not detect vulnerabilities and requires regular updates.

HA Python requirements use hashes and WAL-G downloads verify per-architecture
SHA-256. Apt packages in that manual HA image still follow the distribution
repository; their inventory and image scanning remain separate maintenance work.
The illustrative `ghcr.io/example/posts-api:latest` deployment is not a published
Hyrule image; adopters must supply and pin their actual image digest.

See [releasing.md](releasing.md) for both commands' eight binary artifacts and
strict attestation verification. Binary and git-archive provenance does not
attest the canonical Go proxy module zip. No release is published by this change.

## Administrative follow-up and review boundaries

After CI passes, separately configure main's active ruleset to require PRs,
resolved conversations, an up-to-date branch, and these existing checks:

- Go 1.26.x integration
- Go 1.27.x integration
- Documentation EN and PT (includes website audit/security regressions)
- Windows SQLite and CLI
- CodeQL Go
- Go vulnerabilities

Verify exact check names and bind the expected GitHub Actions app where available.
Keep force-push/deletion prevention and no permanent broad bypass. With one
maintainer, use zero required independent approvals; AI assistance is not a human
reviewer. Revisit review requirements when real maintainers join. Do not require
the aggregate Scorecard result or the periodic fuzz campaign on every PR.
Also enable/verify Dependabot alerts/security updates separately. Existing weekly
version-update PRs are a different feature. No GitHub settings change here.

Gui-Rondon owns security triage and the initial response within seven calendar
days, with AI assistance as useful. Review coverage during absences. The response
target is not a remediation deadline. See [SECURITY.md](../SECURITY.md).

Suggested review units: readiness (existing fix), npm updates plus fork,
Postgres/pagination fixes with regressions, image digests, audit integration,
fuzz targets/campaign, then release expansion and instructions. Preserve the
matrix, race detector, real integrations and 95% framework coverage gate.

## Local validation on 2026-11-16

- Go 1.26.11: full suite with race and real Mongo/Postgres/Pub/Sub/Redis.
- Go 1.27.4: full suite with race and framework coverage **95.4%**, above 95%.
- Vet and both external-example test targets passed. Govulncheck found no known
  vulnerabilities with Go 1.27.4 in the root and both example modules, or with
  Go 1.26.11 in the root.
- Clean npm install, complete audit, Node 20/22 security regressions, and EN/PT
  production build passed. The upstream deepmerge tarball reproduced inherited
  prototype poisoning; the patched source did not, and its upstream hash matched.
- Each of the four fuzz targets passed a one-minute local smoke campaign. This
  validates execution, not the quality or outcome of future 20-minute campaigns.
- All eight release binaries cross-compiled with Go 1.27.4 and checksums matched.
  These are local test artifacts, not a published/attested release; the working
  tree is uncommitted and git archive still represents HEAD. Native Windows
  execution and fresh CodeQL analysis remain for GitHub CI.
- Actionlint, shell syntax and git diff whitespace checks passed. No GitHub
  protection, alert state, release or other administrative setting was changed.
