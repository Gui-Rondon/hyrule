---
sidebar_position: 20
---
# SQLite for local experimentation

SQLite is an experimental backend, available in the next release containing this
work. `hyrule new my-api` generates a single API; `hyrule service create products`
adds a registered resource and downloads dependencies. `go run .` serves on port
8080. Data persists in hyrule.db; SQLITE_PATH overrides it. No Docker or C compiler
is required. Without CONFIG_FILE or CONFIGSERVER_ADDRESS, local authentication
is disabled. Supply configuration explicitly when exposing the service.

`app.BackendSQLite` uses a caller-owned `Deps.SQLite` in Build; Run opens and
closes its own connection. Resource setup calls
`restsqlite.NewAdapter(ctx, env.SQLite, table, resource)`, assigns Resource.Adapter,
and registers it. The API uses SQLiteTx so hooks and multi-document inserts share
the same transaction. Nested same-database transactions join it; failures roll back.

Supported: CRUD, validation, UUID IDs, pagination, unique scalar indexes, integer
version ETags with compare-and-swap, RFC 7396 merge patch, top-level projection and
sorting, scalar equality/comparisons and AND. Fields/table names must be simple
identifiers. Hyphens in generated resource names map to underscores in table names.
Object/array values can be stored but cannot be compared or uniquely indexed.
Client values are SQL parameters; operators and identifiers are validated.

Unsupported: soft delete, advanced Mongo/Postgres operators, outbox, dry run
and database migration/parity guarantees. `resources.yaml` supports this
backend via `backend: sqlite` (see [Resource via YAML](resources-yaml.md)),
subject to the same restrictions listed here. Unsupported query
operators return 400; unsupported backend configuration fails at startup. Unique
indexes are real database constraints. Missing and null fields both compare as
SQL NULL; this is not Mongo query parity. A single connection serializes local
writers; this backend is for exploration rather than production scalability.

Tables/indexes are created on registration. CREATE IF NOT EXISTS is not schema
migration: existing files need explicit migration when resource definitions change.
Deleting a local file resets the experiment and loses its data.

## CLI migration

`hyrule new` now defaults to the single SQLite API; use `--layout=platform` to
preserve multi-service Mongo generation. `hyrule.json` records the selection.
Existing platform projects without it continue working. Released CLI builds pin
their own framework version; development builds require an explicit compatible
`--framework-version`. No resource generation overwrites existing files, including
collisions between names that normalize to the same identifier.
