---
sidebar_position: 0.5
---

# Integration review: Mongo and Postgres

Review date: **2026-11-15**. Integration baseline: `a4fef45` (merge #44);
main: `4aa6195`; merge base: `bf3383b`. This document records the comparison,
compatibility boundaries and reproducible evidence. It does not authorize a
merge into main or claim a deployment/production performance certification.
The consolidation PR continues to target `integration/postgres-portability`.

## Comparison with main

Review covered the bootstrap and dependency lifecycle, the Store/Adapter seam,
IDs/ETags/CAS and error classification, numeric/schema behavior, transactions
and outbox, index/query opt-ins, migrations and manual HA gates, public API
changes and current EN/PT instructions. Both three-dot history and direct tip
comparison were inspected: main has the independent e2e harness commit
`048bc22`, so checking only integration-added commits would be insufficient.

`git merge-tree --write-tree --name-only 4aa6195 a4fef45` reports **two add/add
conflicts**, without changing a branch or working tree:

- `examples/blog-apis/e2e/run.sh`
- `examples/blog-apis/e2e/jmeter/hyrule-e2e.jmx`

The integration versions retain the Mongo service paths and add Postgres
comments, CAS, isolated Pub/Sub subscription provisioning/cleanup, explicit
migrations and the check-results validator. In an eventual authorized merge,
compare both tips again and preserve those additions together with main's
original Mongo scenarios. Do not resolve by dropping either backend. After
resolution, run the full suite and the 42-sample JMeter/PubSub gate on the
**resolved tree**; a passing integration checkout is not evidence for a future
merge result. Makefile merged automatically in this preview; this is not a
promise that it remains conflict-free after either branch changes.

## Findings addressed by this consolidation

| Finding | Resolution and validation |
|---|---|
| README/Learn/reference still called outbox or dry run Mongo-only; ADR-0003/roadmap said the Postgres outbox was unbuilt | Parallel Mongo/Postgres guide and updated EN/PT outbox, dry-run, testing, architecture and ADR status. Historical decisions remain labeled as history. |
| Redis scheduling text incorrectly implied a shared Mongo/Redis transaction | Clarify local outbox commit and asynchronous relay delivery; Redis/PubSub effects are retryable external effects, not distributed transactions. |
| Unknown `Spec.Backend` silently selected Mongo (and could panic with wrong dependencies) | Reject unknown values in Run/Build before config/auth/database setup. A regression test failed before the fix; valid default Mongo and explicit Postgres are checked through real HTTP CRUD. |
| Pool duration settings had incomplete adoption-path coverage | Real pool configuration assertions cover lifetime, idle, health period and connect timeout; table-driven rejection tests cover every exposed pool setting before dialing. |
| `Spec.DefaultDB` comment implied fallback for a Postgres URI without a database | Document actual pgx resolution and require an explicit database in sample URIs; no existing connection-resolution behavior changed. |
| Bootstrap refactor changed main's defer order: Mongo could disconnect before worker cleanup on early Run failure | Cancel and join Mongo workers before Disconnect, matching Postgres. A real two-backend Setup-failure regression test reproduces the old Mongo failure and verifies DB availability during cleanup. |
| External standalone module was missing pgx checksums required by the new bootstrap dependency graph | Refresh its go.mod/go.sum using go mod tidy, then rerun its real Mongo test; no root dependency versions changed. |

## Compatibility and migration notes

- Existing `Resource.Store` implementations remain BSON-shaped and are wrapped
  by the internal Mongo adapter when `Resource.Adapter` is nil. Mongo remains
  `app.BackendMongo`, the zero value (`""`). Explicitly use that constant;
  `Backend("mongo")`, spelling errors and unknown values are now rejected.
- `rest.Adapter` is the JSONB bridge through `restpostgres`; Postgres Store does
  not implement rest.Store. The service Backend and registered resource adapter
  must agree on their database/transaction dependency. Switching Spec.Backend
  alone does not migrate documents, IDs, indexes, hooks or datasource options.
- **Source changes versus main:** `WriteOptions.IfMatch` is now `rest.IfMatch`,
  and Service lookup/query arguments are `any` instead of BSON-specific types.
  Existing BSON arguments still pass, but typed function values, interface
  implementations/mocks and direct string IfMatch literals need recompilation
  or updates. `events.Hooks`/ConditionalHooks and outbox health functions now
  accept narrow shared interfaces; ordinary pointer calls remain valid, but
  exact function-type assignments may need updating. Exported structs gained
  fields; prefer keyed literals. Follow the pre-1.0 compatibility policy in
  CHANGELOG/ADR-0005 rather than advertising a drop-in source upgrade.

```go
match, ok := rest.ParseIfMatch([]string{`"` + etag + `"`})
if !ok { return rest.ErrPreconditionRequired }
opts := rest.WriteOptions{IfMatch: match}
```

- Treat IDs/ETags as opaque: Mongo uses ObjectID/content hashes; Postgres uses
  UUID/version. HTTP strong/list/wildcard If-Match parsing is explicit; weak
  tags never strong-match. Mongo keeps its existing single-attempt wildcard
  conflict handling and driver transaction retry labels. Postgres retries CAS
  conflicts only for wildcard PATCH within configured attempt/time budgets.
- PostgreSQL PATCH follows RFC 7396 (`null` removes keys); Mongo keeps Eve
  deep merge/null behavior. Postgres checks required fields after merge;
  `RequireMergePatchContentType` remains explicit opt-in. Schema integer parsing
  preserves int64 precision and rejects overflow; this does not make every
  numeric/storage representation identical across databases.
- Datasource Filter/DefaultSort/Projection, additional BSON lookup features and
  YAML/scaffold output remain Mongo-specific. Postgres schema kinds drive its
  query compiler; nested sorts remain textual. Authentication still validates
  JWTs and does not implement sessions or refresh-token state.

## Opt-ins and accepted limits

Use the [backend adoption table](backends.md#adopt-postgres-opt-ins-deliberately)
and [adapter details](adapter.md). No production resource is opted into typed
sort indexes, custom plan policy or safe datetime sorting by this review.
Defaults, strict datetime failures, legacy index DDL and Mongo contracts remain
preserved for valid configurations.

Known limits: generic field parameters may prevent expression/partial-index
use; deep offsets can require Sort; sparse guards are explicit, because implicit
ones regressed UUID plans. Typed indexes can reject existing/future bad casts,
require new names, and reject Unique/Sparse/datetime combinations. Existing
same-name indexes are not checked for equivalent definitions. Safe datetime is
per-adapter opt-in and retains helper normalization/NULL behavior. Custom plans
cost planning work across the whole pool. None guarantees production latency.

Both outboxes are at-least-once. Mongo uses a leader lease and unbounded retry;
Postgres supports SKIP LOCKED workers, dead_at/replay and no cross-worker Key
ordering. Postgres retention and dead-event monitoring need operator policy.
Dry run rolls back transactional business writes, not raw pool calls, setup
DDL, external effects or sequence allocation. Migrations remain explicit;
startup verifies compatible comments schema but does not advance migrations.

A remaining validation limitation is the existing real-conflict retry test's
60ms budget: it intermittently reports 503 without `RetryExhaustedReason` with
a query-timeout log; the precise cause is not fixed by this review. It failed on a full
run during this review; this is recorded rather than treated as proof of a
new shutdown regression. Keep isolated/rerun results distinct from a fix to
retry classification. The consolidation does not change that retry contract.

## Reproducible gate evidence

All databases are real. Plan gates seed 20,000 rows and keep scan settings on.
These counts describe the checked fixtures, not every query/index or a capacity
benchmark. Auto/generic observations must not be generalized to all workloads.

| Gate | Expected evidence | Local artifact |
|---|---|---|
| `postgres-ha-test` | Topology, replica replay and WAL archive | Go output; manual local lab |
| `postgres-ha-migrations-test` | Expand/backfill/contract, dirty/version checks and real HTTP | Go output; isolated schemas |
| `postgres-ha-explain-test` | 30-plan A6 observational inventory and catalog controls | `.local/postgres-explain/` |
| `postgres-ha-where-test` | 30 plans; selective Index Cond in auto/custom; generic datetime helper evaluated once per source row | `.local/postgres-explain/` |
| `postgres-ha-id-sort-test` | 18 plans, 12 PK-without-Sort checks; deep offset observational | `.local/postgres-id-sort/` |
| `postgres-ha-sparse-test` | 30 plans; 2 explicit-presence Index Cond checks | `.local/postgres-explain/` |
| `postgres-ha-typed-sort-test` | 48 plans, 24 auto/custom index-without-Sort checks, complete result parity | `.local/postgres-typed-sort/` |
| `postgres-ha-plan-policy-test` | 12 actual cached statement plans; URI policy on two connections/transactions | `.local/postgres-plan-policy/` |
| `postgres-ha-datetime-sort-test` | 48 plans, 24 checks; strict default versus explicit safe mode, two zones | `.local/postgres-datetime-sort/` |

```sh
make test GOFLAGS='-count=1 -p=1'
make vet
make cover GOFLAGS=-p=1
make standalone-example-test
make postgres-ha-status POSTGRES_HA_PORT=5540
make postgres-ha-test POSTGRES_HA_PORT=5540
make postgres-ha-migrations-test POSTGRES_HA_PORT=5540
make postgres-ha-explain-test postgres-ha-where-test postgres-ha-id-sort-test \
  postgres-ha-sparse-test postgres-ha-typed-sort-test postgres-ha-plan-policy-test \
  postgres-ha-datetime-sort-test POSTGRES_HA_PORT=5540
# Only after tests/coverage (Docker builds compete with short retry budgets):
make e2e
cd website && npm run build
```

Reports contain deterministic fixture SQL/data rather than credentials. For
report-based gates, inspect `report.json.complete`, plan/check counts, errors
and captured SQL; a Go PASS alone is insufficient if the expected matrix is
incomplete. Each schema is removed before a complete report is published.
The normal coverage profile excludes disabled manual HA paths and testkit;
coverage percentage and plan counts measure different things.

Failover and PITR were validated in the earlier local A2/A3 work and their
harness/runbooks remain in `infra/postgres-ha/`. They are historical evidence,
not rerun by the consolidation's routine plan gates. Their commands kill a
primary or create a restore sandbox and require deliberate manual execution.
No HA provisioning/execution is added to GitHub Actions, and 5434 belongs to
another local project. The linked [HA runbook](https://github.com/Gui-Rondon/hyrule/blob/integration/postgres-portability/infra/postgres-ha/README.md)
records scope and operational limits; it is not a production deployment design.

## Consolidation execution

Fresh consolidation runs passed: full race suite (`-count=1 -p=1`), vet,
real Mongo external-module test, topology, migrations, seven complete plan
reports (**216 plans**), compiled Mongo/Postgres documentation programs and
JMeter/PubSub (**42 samples, zero failures**). Coverage rose from **85.5% to
85.9%** (85% gate); Connect coverage rose from 73.9% to 95.7%, and Env.Wrap /
finishBuild are now 100% in the measured profile. EN/PT website build passed.

The [versioned evidence snapshot](https://github.com/Gui-Rondon/hyrule/blob/integration/postgres-portability/infra/postgres-ha/evidence/2026-11-15-consolidation.json)
records per-gate counts, mode observations and hashes of the local reports.
Failover/PITR are explicitly historical, not part of these fresh runs.

Full local raw artifacts remain ignored under `.local/backend-consolidation/`;
the versioned record above provides commands and acceptance criteria for
reproduction. Local data, uncommitted plans and credentials are not dependencies
of this documentation. A future merge into main remains a separate decision.
