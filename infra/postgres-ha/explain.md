# Manual declared-index EXPLAIN gate (A6)

```sh
make postgres-ha-up POSTGRES_HA_PORT=5540
make postgres-ha-explain-test POSTGRES_HA_PORT=5540
```

The gate requires the real lab already running. It is manual/local only, with
its own `POSTGRES_EXPLAIN_TEST=1` opt-in. No provisioning or execution in GitHub
Actions is added. Run one lab gate/command at a time. Port 5434 is not used when
5540 is selected; other projects and their databases are untouched.

## Inventory and data

The test reads `spec().PostgresIndexes` from the actual comments-api code. Every
declaration must have a scenario; a new declaration without a matching case
fails. Fixture declarations supplement the current single JSONB post_id index
with named unique/sparse, compound/DESC and datetime indexes. They are created
by the real `postgres.EnsureIndexes`; datetime functions use EnsureFunctions.
The resource table is prepared by its actual version 1/2/3 migrations.

Each run creates one private schema, with 20,000 deterministic documents in
each of the comments and fixture tables. Half the comments share a hot post;
the other post IDs are selective. Fixtures have unique slugs on 75% of rows,
100 categories, increasing ranks and varied timestamps. Business fields are deterministic; UUIDs and metadata timestamps are local to
each run, so fingerprints compare modes within a run. ANALYZE runs after
loading and creating indexes. No production or public table is sampled.

Nine cases cover selective/common comments lookups, present/missing sparse
keys, compound ordering/pagination, selective/common datetimes, a selective
lookup after five hot-post warmups, and a primary-key
lookup control. That control can use an unrelated index; it must not be
reported as use of the expected declared index. Plan choices remain observations.
The sparse absent-key case deliberately does not imply its partial predicate.

## Query and plan fidelity

A pgx QueryTracer observes the SELECT actually executed by a real
restpostgres adapter's Find, including compiled filters, sort and pagination.
It does not replace a database operation, duplicate the query compiler or
change production SQL. Expected counts, fixture values and compound ordering
are checked. The captured SELECT is prepared on a pinned database session.

Each case runs in three local transaction modes: auto, force_custom_plan and
force_generic_plan. Auto first executes five warmups so PostgreSQL can make
its normal generic/custom decision; this is not a claim about a cold first
request. Custom/generic counters are sampled immediately before and after
EXPLAIN to identify the plan actually explained. A parameterless prepared
statement is always generic, even when force_custom_plan is selected.

`EXPLAIN (ANALYZE, BUFFERS, SETTINGS, FORMAT JSON) EXECUTE ...` runs the prepared
SELECT. EXECUTE arguments are rendered safely by pgx's simple protocol, not by
an ad-hoc literal interpolator. The same prepared query is also executed to
compare returned IDs with the real adapter result: as an ordered sequence for
sorted cases, otherwise as a sorted set. Root actual row count must match.

All modes use read-only transactions. SET LOCAL changes only plan_cache_mode;
rollback restores it and prepared statements are deallocated. The harness does
not disable sequential/index/bitmap scans or change global settings. It checks
those scan options are enabled and records planner costs, memory, statistics,
JIT and timezone settings along with server version and system identifier.
Queries and cleanup have bounded contexts and session timeouts.

## Acceptance and reports

The gate fails on missing cases, duplicate case names, absent/invalid/not-ready
indexes, SQL failures, wrong results, unverified prepared mode or incomplete
reports. It walks all nested plan nodes and compares exact index names,
including Bitmap Index Scan; another index or a node containing the word
"Index" does not satisfy the expected declaration.

Planner non-use is **not a test failure**. Each result says
`expected_index_used` or `expected_index_not_used` and lists actual indexes and
scan types. Stats include estimated/actual rows, planning/execution times,
shared hits/reads and prepared-plan counters. Root buffer counts include child
nodes, so the reporter does not sum the tree and double-count blocks.

After all normal plans, real negative catalog controls remove one fixture
index and provoke a failed concurrent unique index build on duplicate values.
The resulting missing and invalid indexes must be rejected by the reporter.
These changes affect only the run's disposable schema. Unit tests additionally
check omitted declarations, duplicate cases and nested/exact-name extraction.

Artifacts are written under `.local/postgres-explain/<UTC-time>-<UUID>/`:

- `report.json`: declarations, scenarios, settings, observations, full plans,
  correctness fingerprints, negative controls, errors and `complete` status.
- `plan-*.json`: complete PostgreSQL EXPLAIN JSON for each observation.
- `summary.md`: human-readable plan/index/scan/row/buffer/timing table.

Successful runs currently produce 27 plans (9 cases x 3 modes). Errors preserve
a partial report with `complete=false`; file-write errors fail the gate. The
completion flag is atomically published only after database cleanup and all
artifact writes, so a failed summary write cannot leave a success marker. Only
synthetic SQL/arguments are written, never a connection URI or password.
Artifacts are gitignored and retained for local inspection. Cleanup drops only
the generated schema. If the test process is forcibly terminated, use its
logged schema name to inspect and remove that schema; do not reset the lab.

## Focused WHERE regression gate

```sh
make postgres-ha-where-test POSTGRES_HA_PORT=5540
```

This separate opt-in (`POSTGRES_WHERE_TEST=1`) runs the same actual-adapter
matrix with focused assertions for the predicate correction. Selective comments
and datetime cases must produce an Index Cond on the expected index in custom
and auto modes; comments also uses five hot-post warmups before the rare value.
Warmups are captured from the real adapter, must have identical SQL shape, and
verify their own rows/ID fingerprints. Reports record the warmup bindings/count.

A read-only generic-plan control locally enables function tracking and compares
CompileFilter with CompileWhere: each must call the datetime helper once per
source row (20,000 calls, not 40,000), with identical matching counts. Tracking
and planner mode are SET LOCAL and rolled back. Regression checks are recorded
in report.json. The general A6 gate remains observational; generic index non-use,
sparse predicates and typed sort differences are outside this correction.

## Observations and limits

The PR #38 baseline on local PostgreSQL 16.16 chose sequential scans for the
seven declared-index query scenarios in all three modes, including selective lookups and custom
plans. The direct UUID lookup control is reported separately; its actual index
choice is recorded without being counted as use of the declared compound index. These are reported
accurately while result checks pass; this is evidence of current query/index
usage, not a successful index optimization.

Custom-plan JSON shows field parameters replaced by constants while the
compiled predicate retains its `IS TRUE` wrapper. The sparse lookup also lacks
an explicit partial-index predicate, and sort expressions can differ from
indexed JSONB expressions. The observations do not establish a single causal
explanation or claim every parameterized expression is unindexable. Compiler,
index and query-shape changes need a separately scoped decision.

Synthetic selectivity, warmups, local cache and a small dataset are not
production RTO, latency/throughput guarantees or capacity planning. This gate
covers Go IndexSpec declarations, not every physical primary-key/outbox index,
all possible filters, cold-cache behavior, concurrent load or planner versions.
It changes no public API, compiler, production index definition or migration.
Merge of integration/postgres-portability into main remains a separate decision.

After the WHERE correction, rerun the matrix to inspect current choices:
CompileFilter keeps its original scalar boolean, while the adapter uses the
shared CompileWhere path. Positive comparisons can become index conditions and
DSL NOT applies IS NOT TRUE, without duplicating the safe datetime expression.
The historical baseline above describes the pre-correction behavior.

References: [EXPLAIN](https://www.postgresql.org/docs/16/sql-explain.html),
[prepared plans](https://www.postgresql.org/docs/16/sql-prepare.html),
[planner settings](https://www.postgresql.org/docs/16/runtime-config-query.html).
