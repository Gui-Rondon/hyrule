# Local Postgres HA lab (A1)

This opt-in lab exercises real PostgreSQL streaming replication and WAL-G S3
archiving. It is run **manually, locally**. It has no GitHub Actions workflow and
is not started by `make env-up`, `make test` or the blog-apis harness.

## Start and verify

Requires Docker Engine with Compose v2, the repository's Go toolchain, internet
access for the initial image build, and roughly 3 GB of available memory. Run
from the repository root:

```sh
make postgres-ha-up
make postgres-ha-test
make postgres-ha-status
make postgres-ha-logs
make postgres-ha-down
```

The proxy defaults to port 5434. If another project already uses that port:

```sh
export POSTGRES_HA_PORT=5540
make postgres-ha-up
make postgres-ha-test
make postgres-ha-status
```

Use the same variable for every invocation. Direct node and Patroni ports are
fixed as listed below. A bind conflict fails startup rather than stopping any
other project. All Make targets and test diagnostics select the fixed Compose
project `hyrule-postgres-ha`, even if `COMPOSE_PROJECT_NAME` is set for another
application. The root development stack on port 5433 is independent.

```text
Go application / smoke test -> HAProxy -> current primary -> two standbys
                                |         Patroni on each PG node
                                |                   |
                                +-- /primary     etcd quorum (3 members)

current primary -> WAL-G archive_command -> SeaweedFS S3 bucket
```

| Service | Host endpoint | Purpose |
|---|---|---|
| HAProxy | `127.0.0.1:5434` (or `POSTGRES_HA_PORT`) | Stable writer endpoint |
| pg1, pg2, pg3 | `127.0.0.1:5435`, `:5436`, `:5437` | Direct SQL inspection |
| Patroni pg1, pg2, pg3 | `127.0.0.1:18008`, `:18009`, `:18010` | Role, health and configuration |
| SeaweedFS S3 | `127.0.0.1:8334` | Local S3 API (internal `s3:8333`) |
| etcd1, etcd2, etcd3 | Container network only | Patroni coordination |

SQL credentials are `postgres` / `postgres-ha-local`, database `postgres`:

```sh
psql 'postgres://postgres:postgres-ha-local@127.0.0.1:5434/postgres?sslmode=disable'
```

These are committed, **development-only** credentials. All published listeners
bind to loopback. Patroni/etcd use an isolated Compose network, without TLS or
production authentication hardening. Never expose the lab to an untrusted
network. It does not read the host's AWS configuration or credentials.

## Cluster policy and process ownership

Each Postgres node has its own data volume. Roles are dynamic: do not assume
`pg1` is primary. The image's original PostgreSQL entrypoint is replaced: a
small root entrypoint prepares volume permissions, drops to `postgres`, then
execs Patroni, which alone owns PostgreSQL initialization and lifecycle.

Patroni uses three etcd members and the following dynamic configuration:

- `synchronous_mode=true`, `synchronous_mode_strict=true`,
  `synchronous_node_count=1`, and PostgreSQL `synchronous_commit=on`.
- A successful normal commit waits for WAL flush on the primary and one
  synchronous standby. The other standby may still be replaying. With no
  eligible synchronous standby, commits block instead of silently degrading
  to local durability. The smoke test waits for both standbys to replay its
  marker before checking their contents.
- `ttl=30`, `loop_wait=5`, `retry_timeout=5`, timeline checks, replication slots,
  data checksums and `pg_rewind`. Patroni manages `synchronous_standby_names`.
- `pg_basebackup` initializes standbys. Slots retain at most 512 MB of WAL;
  prolonged outages may require reinitialization. No container restart policy
  hides the process failures that A2 will test.

HAProxy checks Patroni's `/primary` endpoint, routes new TCP connections to the
leader and closes sessions when a backend is marked down. It does not retry
transactions or preserve in-flight sessions. A2 will measure reconnection and
new writes after actually killing a node; A1 does not claim that guarantee.

Bootstrap DCS settings are applied only on initial cluster creation. Editing
`bootstrap.dcs` in YAML does not update an existing cluster. Inspect with
`patronictl show-config`; for this disposable lab, use the explicit full reset
below to apply changed bootstrap defaults.

## Archive storage and the smoke test

SeaweedFS `weed mini` provisions bucket `hyrule-postgres-ha` with access key
`hyrule-local` and secret `hyrule-local-secret`. WAL-G uses the shared prefix
`s3://hyrule-postgres-ha/cluster`, explicit region `us-east-1`, internal endpoint
`http://s3:8333` and path-style requests. All nodes belong to the same Postgres
system identifier, so they share one archive namespace across promotions.

Postgres uses `archive_mode=on`, `archive_timeout=30s`,
`archive_command='wal-g wal-push "%p"'` and
`restore_command='wal-g wal-fetch "%f" "%p"'`. Standbys stream first during
normal operation and have WAL-G available for archive recovery. They may log
that the newest, not-yet-archived segment is absent before switching to
streaming; that alone does not mean primary archiving failed. When promoted,
a node's archiver can continue using the same S3 prefix.

`make postgres-ha-test` sets `POSTGRES_HA_TEST=1` and runs a real Go test with
`-race -count=1`. Without that opt-in, ordinary `go test ./...` skips it. With
opt-in but missing/broken infrastructure it fails, rather than skipping.
The test:

1. Checks a single leader with a Patroni lock, two streaming standbys, matching
   system identifiers, the actual strict synchronous policy and at least one
   synchronous standby.
2. Connects through the proxy using `platform/postgres.Connect`, checks the
   target is writable and inserts a marker into a uniquely named test table.
3. Waits for both standbys to expose that marker; direct writes to standbys
   must be rejected with PostgreSQL's read-only SQLSTATE `25006`.
4. Forces a WAL segment switch, records the completed source segment's SHA256,
   waits for the archiver, fetches that segment from S3 into a temporary file
   and checks the SHA256 matches. This checks the real archive command and the
   stored bytes, not just the existence of an object or configuration string.
5. Removes only its table and temporary download. On failure it includes
   bounded Compose status/log diagnostics; the lab remains available to inspect.

No AWS Go SDK or Docker library is added to the framework. Test code uses pgx,
HTTP and `os/exec` with argument arrays for the Docker Compose CLI. Bounded
contexts and polling establish readiness; fixed sleeps are not acceptance
criteria. `POSTGRES_CONNECTION_STRING` is cleared only inside the test so an
application's environment cannot redirect it away from the lab.

## Persistence, reset and diagnosis

`make postgres-ha-down` preserves Postgres, etcd and S3 volumes together. A
subsequent `make postgres-ha-up` reuses the same cluster and archive history.

```sh
# Destructive: removes only this lab's Postgres, etcd and S3 volumes.
make postgres-ha-reset
make postgres-ha-up
make postgres-ha-test
```

Reset all three kinds of storage together. Keeping a previous cluster's
archive prefix while reinitializing unrelated Postgres data can mix histories.
There is no automatic reset on test failure, no retention job and no deletion
from an external S3 service. The archive grows until the explicit reset.

For diagnosis, `make postgres-ha-status` shows membership and archiver counters
for each node, and `make postgres-ha-logs` shows bounded logs. Inspect a node:

```sh
docker compose --project-name hyrule-postgres-ha -f infra/postgres-ha/compose.yml \
  exec -T --user postgres pg1 patronictl -c /etc/patroni.yml show-config
docker compose --project-name hyrule-postgres-ha -f infra/postgres-ha/compose.yml \
  exec -T --user postgres pg1 psql -U postgres -d postgres \
  -c 'SELECT application_name, state, sync_state, sent_lsn, flush_lsn, replay_lsn FROM pg_stat_replication'
```

Run the replication query on whichever node `patronictl list` identifies as
leader. A blocked write with no synchronous standby is expected under strict
mode. Failed `wal-push`, growing `failed_count` or stalled archiving warrants
checking the S3 logs, credentials, free disk space and bucket availability;
never replace `archive_command` with a command that always returns success.

## Pinned components and boundaries

- PostgreSQL `16.16-bookworm`, Patroni `4.1.5`, psycopg `3.2.10`.
- WAL-G `v3.0.9`, with official binary SHA256s for Linux AMD64 and ARM64.
- etcd `v3.6.16`, HAProxy `3.2.8-alpine`, SeaweedFS `4.53`.
- Base/runtime images are pinned by manifest digest in Dockerfile/Compose.
  Python transitive dependencies are pinned with hashes in `requirements.lock`.
  Apt resolves Debian packages during build; the image is not claimed to be
  byte-for-byte reproducible.

To refresh the Python lock deliberately, use Python 3.11 with pip-tools 7.5.1
and run `pip-compile --generate-hashes --output-file=requirements.lock requirements.in`.
The image installs it with `pip --require-hashes`. Version/digest changes need
another real build and smoke run. Linux AMD64 is the locally verified platform;
ARM64 has a pinned WAL-G artifact but is not claimed tested here.

A1 proves healthy topology and WAL archival round-trip only. Base backup and
PITR restoration are A3; killing a primary and testing application recovery
is A2. The single-host lab has one proxy and one S3 service: it does not prove
host-failure tolerance or end-to-end production HA. No migration, public Go
API, ordinary development Compose or GitHub Actions workflow changes are made.

Sources: [Patroni replication policy](https://patroni.readthedocs.io/en/latest/replication_modes.html),
[Patroni REST API](https://patroni.readthedocs.io/en/latest/rest_api.html),
[Postgres archiving](https://www.postgresql.org/docs/16/continuous-archiving.html),
[WAL-G storage](https://wal-g.readthedocs.io/STORAGES/),
[SeaweedFS mini](https://github.com/seaweedfs/seaweedfs#quick-start).
