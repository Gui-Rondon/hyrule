# comments-api migrations (A5)

The application checks a clean, compatible schema at startup; it no longer
creates its comments table. Run migrations explicitly before deployment. The
runner pins golang-migrate v4.20.1, using its pgx/v5 driver and embedded SQL.
The HTTP API, JSONB document, version and etag contract stay unchanged.

Run from the repository root with an explicit connection string. The target
schema must already exist. `-schema` defaults to `public`; all commands scope
every connection to that schema alone. The application connection must select
the same schema when using a non-public schema. `version` only reads metadata
and does not create the migration table. Do not run different rollout stages
concurrently. Run one operator command at a time, even though the migrator and
backfill share a database advisory lock.

```sh
export POSTGRES_CONNECTION_STRING='postgres://postgres:postgres@localhost:5433/postgres?sslmode=disable'
make comments-migrate MIGRATE_ARGS='-action version'
make comments-migrate MIGRATE_ARGS='-action up -to 1'
# Start/keep the compatible HTTP binary running against version 1.
make comments-migrate MIGRATE_ARGS='-action up -to 2'
make comments-migrate MIGRATE_ARGS='-action backfill -batch-size 100'
make comments-migrate MIGRATE_ARGS='-action up -to 3'
make comments-migrate MIGRATE_ARGS='-action version'
```

For a local setup or the existing e2e harness, the explicit `-action prepare`
command performs those stages in order, resuming at the recorded clean version.
It does not repair dirty state. Calling it again at version 3 is a no-op. The
staged commands above let an operator inspect data and control the backfill
window between deployments. Starting the HTTP process never runs either path.
Startup reads migration state and the table only from the selected current
schema, without accepting a later search-path schema as a fallback. The
service accepts versions 1, 2 and 3; future versions and dirty state fail
startup with an actionable error. Outbox setup and shared functions/indexes
retain their existing lifecycle and are outside these table migrations.

## Baseline / adoption (version 1)

Migration 1 creates the six-column adapter table on a fresh database. If a
legacy comments table exists, the migration validates column count, types,
nullability, defaults and primary key before recording a clean version. It
preserves rows and existing indexes; it does not mark an arbitrary table as
migrated via `force`. A mismatched legacy table fails adoption and must be
inspected. Defaults equivalent but expressed differently may be rejected by
the deliberately strict baseline validation. Do not edit old migrations to
match an unrelated table. This is not a general-purpose schema drift detector.

## Expand, backfill, contract

Version 2 adds a nullable `post_id text` projection of `doc->>'post_id'` and a
BEFORE INSERT OR UPDATE trigger that always derives it from the document. Old
and current JSONB writers remain compatible, including PATCH and direct
updates to the projection. Existing rows remain null until backfill; new or
changed rows are synchronized immediately.

The separate backfill validates that legacy documents contain string post IDs,
then updates at most `-batch-size` rows per autocommit. It holds the migrator's
actual schema-specific advisory lock while progressing, so DDL stages cannot
race it. API writes can continue between short row-locking batches. It does
not change `doc`, `version`, timestamps or etags. A canceled/failed backfill
retains committed batches and can be rerun; already synchronized rows are
skipped. Missing/invalid legacy post IDs require their actual references to be
repaired, never an invented default. Batch sizes are restricted to 1..10000.

Version 3 validates a not-null check, sets NOT NULL using that validated check,
and validates a constraint requiring string JSON post IDs and equality with
the new column. The compatibility trigger remains permanently necessary while
the adapter writes JSONB. It is not removed at contract. No JSON field or
public API field is renamed. The existing JSONB lookup index is unchanged;
query-plan analysis is A6.

DDL uses transactions and bounded lock/statement waits. Baseline adoption and
ALTER TABLE can briefly require strong table locks; a small local test is not
proof of zero downtime or a production lock budget. This example does not add
concurrent index builds, migration scheduling or a production migration role.

## Failure and explicit repair

The runner never forces migration state automatically, and `up` refuses a
backward target (also checked after acquiring the migration lock). Each SQL
migration is transactional; the golang-migrate dirty marker is outside that
transaction. A failed migration can therefore leave dirty state even after
its DDL rolled back. Close the failed runner before inspecting the database.

For example, if version 3 was attempted before backfill:

1. Read `version` and inspect columns, constraints and trigger state. Confirm
   migration 3 rolled back and the physical table is exactly version 2.
2. Explicitly repair **only the metadata** to version 2:
   `make comments-migrate MIGRATE_ARGS='-action force -to 2'`.
3. Run backfill, inspect nulls/mismatches, then retry `up -to 3`.

`force` requires an explicitly supplied `-to` and is only accepted while dirty; it does not execute SQL, fix data or
prove the physical version. Do not force to 3 while constraints are absent.
Other failures require identifying the actual physical state before choosing
a version. Do not use these example commands as an automatic recovery script.
Migration 1's down SQL refuses deletion of the baseline table. The supplied
version 2/3 down SQL describes structural reversal, but the operator command
only exposes forward execution and explicit metadata repair. Deployment
rollback normally means running the compatible HTTP binary against the
current schema; dropping the derived column needs a separately reviewed action.

## Manual local gate

```sh
make postgres-ha-migrations-test POSTGRES_HA_PORT=5540
```

This does not provision infrastructure. It requires the real HA lab already
running and creates unique private schemas, removed with their test data at
cleanup. It never touches `public.comments` or another project's database.
`POSTGRES_MIGRATIONS_TEST=1` is the separate opt-in; ordinary tests skip this
rollout gate. No GitHub Actions provisioning or execution was added.

The gate serves the actual resource over real HTTP through one server across
all stages, verifies POST/PATCH, batch/resume behavior and unchanged legacy
etags, checks existing-table adoption and rejects an incompatible table. It
provokes dirty state with a premature contract, proves transactional rollback,
then exercises an explicit operator-equivalent repair. It observes a real
contending migrator in `pg_stat_activity` waiting for its advisory lock and
confirms progress after release. It also refuses another schema's migration
state through search-path fallback. There are no mocks.

The existing comments resource tests now prepare their isolated schemas with
these same migrations. The existing JMeter harness explicitly runs `prepare`
before starting comments-api and continues verifying HTTP and real Pub/Sub.

References: [golang-migrate](https://github.com/golang-migrate/migrate),
[pgx/v5 driver](https://github.com/golang-migrate/migrate/tree/v4.20.1/database/pgx/v5),
[PostgreSQL ALTER TABLE](https://www.postgresql.org/docs/16/sql-altertable.html).
