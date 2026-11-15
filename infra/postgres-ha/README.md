# Local Postgres HA lab (A1/A2/A3)

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
transactions or preserve in-flight sessions. The separate A2 failover test
measures reconnection and new writes after actually killing a node; the A1 smoke alone does not claim that guarantee.

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

## Destructive failover test (A2)

Run manually, locally, **one lab command/test at a time**. Do not run smoke,
failover, PITR, up/down/reset or other writers concurrently. This target does not
start infrastructure; missing or unhealthy infrastructure is a failure.

```sh
make postgres-ha-up POSTGRES_HA_PORT=5540
make postgres-ha-test POSTGRES_HA_PORT=5540
make postgres-ha-failover-test POSTGRES_HA_PORT=5540
make postgres-ha-test POSTGRES_HA_PORT=5540
make postgres-ha-status POSTGRES_HA_PORT=5540
```

`postgres-ha-test` remains the A1 smoke only. The destructive target sets the
separate `POSTGRES_HA_FAILOVER_TEST=1` opt-in and runs only
`TestPrimaryCrashApplicationRecovery`, with `-race -count=1`. Ordinary tests
skip both checks. No GitHub Actions provisioning or execution is involved.

The A2 harness:

1. Requires all three nodes, one SQL/Patroni leader, two streaming replicas,
   strict synchronous policy, `synchronous_commit=on`, matching system
   identifiers and healthy etcd quorum. Checks the proxy points to that leader.
2. Creates a unique document table and starts a real local HTTP server using
   `app.Build`, `restpostgres` and one `platform/postgres.Connect` pool through
   HAProxy. Records IDs and expected content for ten successful HTTP writes.
   It does **not** wait for both replicas to replay those writes before killing.
3. Discovers the primary dynamically, checks the Compose-selected container's
   project/service labels and disabled restart policy, then sends SIGKILL to
   that node's entire container (Patroni and PostgreSQL). Confirms it stopped
   and that an already-open proxy session breaks.
4. Continues bounded HTTP POST attempts using the **same server and pool**.
   Patroni elects another primary without manual promotion; the surviving
   standby must become synchronous. The new leader must have the same system
   identifier and a higher timeline. Each attempt uses a new marker: an error
   or missing response is an unknown result, never evidence of rollback and
   never an instruction to replay that logical operation blindly.
5. Requires multiple confirmed new HTTP writes, HTTP health recovery, and
   correct ID/content for **every confirmed write**, including those before
   the crash, through HTTP and direct SQL on the new primary. The old primary
   remains stopped throughout acceptance. The deadline is 120 seconds from
   the kill command; it is a local acceptance budget, **not a production SLA**.
   Logs report the first observed healthy leader and first confirmed HTTP
   write separately; polling is not an exact election-time measurement.
6. Restarts only the interrupted node in cleanup, with an independent bounded
   context, and waits for it to rejoin as a replica with two streaming standbys
   and healthy etcd quorum. Also checks confirmed contents on the rejoined
   replica. No failback is requested. Drops only the test table after successful
   recovery. A subsequent A1 smoke verifies WAL archival on the new leader as
   well as the restored topology.

On assertion failure, bounded Compose diagnostics are collected **before**
recovery. Recovery is attempted on both success and failure; a failed run before
promotion may recover with the original leader. No automatic volume deletion,
reinitialization or full reset is performed. If recovery
fails, the test fails and preserves its uniquely named table for diagnosis.
Inspect logs and use `make postgres-ha-up POSTGRES_HA_PORT=5540` to restart lab
services with existing volumes. If the Go process is forcibly killed, its
cleanup cannot run: use the same recovery command, inspect topology, and rerun
the smoke. A healthy node startup alone is not proof of completed failover.

This proves recovery from one abrupt primary-container failure while the host,
proxy, etcd quorum, S3 and remaining Postgres nodes are available. Requests can
fail during the outage; in-flight commits with no client confirmation remain
ambiguous. It does not prove continuous availability, exactly-once semantics,
network-partition fencing, multi-node/host/disk failure tolerance or production
RTO/RPO under load. The HTTP resource exercises the shared application path;
it does not run comments-api's Pub/Sub workflow. Base backup/PITR is A3,
migrations A5 and query-plan analysis A6.

## Full backup and point-in-time recovery (A3)

Run this opt-in gate manually and locally, with no concurrent lab commands or
writers. The existing HA cluster and S3 must already be healthy:

```sh
make postgres-ha-up POSTGRES_HA_PORT=5540
make postgres-ha-pitr-test POSTGRES_HA_PORT=5540
make postgres-ha-test POSTGRES_HA_PORT=5540
make postgres-ha-status POSTGRES_HA_PORT=5540
```

`postgres-ha-pitr-test` sets `POSTGRES_HA_PITR_TEST=1` and selects only
`TestBaseBackupPointInTimeRecovery`, with `-race -count=1`. It does not start
or stop the HA cluster. Ordinary tests skip it without opt-in, and the A1/A2
targets keep selecting their own tests. No GitHub Actions changes are involved.

The gate creates a uniquely named source table with a baseline row, takes a
real WAL-G **full** physical backup (`backup-push --full --verify`), and records
the exact newly completed backup name from `backup-list --json`. It uses that
name, never `LATEST`. Page checksum verification is requested during backup;
the acceptance evidence is a successful restore and correct contents, not
just a backup object or sentinel appearing in S3.

After the backup completes, the test inserts and confirms another row. It
then records the **source database's** UTC clock and WAL LSN, waits until that
clock is strictly later than the target, and commits a destructive update plus
a third row. The test closes the WAL segment containing that later commit and
waits for archiving, ensuring the archive contains data beyond the target.
The target is a timestamp with a numeric UTC offset, not a named restore point
or the end of the available WAL. Leadership, system identifier and timeline
must remain unchanged throughout this sequence.

The `pitr` Compose service belongs to an **opt-in profile** and uses the existing
lab image, a fresh anonymous data volume and a dynamically allocated port bound
to `127.0.0.1`. The harness runs it with a unique container name and `--no-deps`;
normal `postgres-ha-up` does not start it. No source data volume is mounted.
The restore script runs as postgres, requires an empty PGDATA and fetches the
explicit backup from S3. It replaces inherited configuration rather than
starting Patroni: no primary connection or replication slot, no synchronous
standby requirement, `archive_mode=off`, and only WAL-G `wal-fetch` for recovery.
The restored copy cannot publish a divergent timeline into the shared archive.
It uses the source database's development password; the dynamically published
listener is never bound to a public interface.

Recovery uses `recovery.signal`, the recorded `recovery_target_time`, an explicit
source timeline, and `recovery_target_action=pause`. Acceptance requires:

- Same system identifier, recovery actually paused, and replay at least through
  the WAL LSN recorded after the post-backup insert.
- No WAL receiver, empty `primary_conninfo`, and archiving disabled.
- Baseline value intact and the post-backup row present: the latter requires
  WAL replay beyond the base backup.
- Destructive update and third row absent: recovery stopped at the chosen time,
  even though their WAL is available in S3.
- Explicit promotion of **only the restored copy**, followed by a confirmed
  independent write. The source still has the later destructive state, lacks
  the restored-only row, and retains its original healthy leader/timeline.

The harness fails immediately if its sandbox exits during startup; merely
accepting SQL connections is not proof that the recovery target was reached.
It bounds the backup process inside Docker to 180 seconds (with forced
termination after five additional seconds), uses bounded polling, and has a
seven-minute overall budget and a ten-minute Go test timeout; these are local test budgets, not production RTO guarantees.

Cleanup closes connections and removes only the generated sandbox container
and its anonymous volume, plus the test's source table. On failure it prints
bounded sandbox logs before removal. Full backups and WAL remain in the local
S3 archive; there is no automatic backup deletion or retention policy. Repeated
runs therefore grow storage until an explicit whole-lab reset. If the test
process is forcibly terminated, cleanup cannot run: use the logged sandbox and
table names to inspect/remove only those resources. `docker rm -f -v <sandbox>`
removes its anonymous restore volume; never remove a pg1/pg2/pg3 volume to clean
up a restore test. No failover or source-cluster reset is required.

This proves restore of a physical full backup and timestamp-targeted WAL replay
from this lab's S3. The recovery uses one source timeline per run; recovery
across a failover during the backup is not claimed. It does not validate
incremental backups, retention, encryption, off-host disaster recovery, backup
scheduling, throughput under production load or the comments-api/Pub/Sub
workflow. Migrations remain A5 and query-plan analysis A6.

## Manual migration rollout gate (A5)

`make postgres-ha-migrations-test POSTGRES_HA_PORT=5540` exercises the actual
comments-api resource and golang-migrate against isolated schemas in this lab.
It requires the healthy lab already running, uses its own opt-in, and does not
run concurrently with other lab gates or writers. See the [migration runbook](../../examples/blog-apis/internal/commentsschema/README.md)
for expand/backfill/contract, adoption, explicit dirty-state repair and limits.
This is a manual local gate; no infrastructure execution in GitHub Actions.

## Manual declared-index plans (A6)

`make postgres-ha-explain-test POSTGRES_HA_PORT=5540` captures the real adapter
SELECT and produces JSON EXPLAIN plans plus a local Markdown summary. It runs
only with its separate opt-in against a disposable private schema. Planner
non-use is reported without failing; infrastructure, coverage of declarations
and result errors fail. See the [A6 runbook](explain.md) for prepared-plan modes,
artifacts, current observations, cleanup and limits. Execution is manual/local;
no GitHub Actions provisioning or execution is added.

## Focused WHERE predicate regressions

`make postgres-ha-where-test POSTGRES_HA_PORT=5540` adds strict selective-index
and datetime-evaluation assertions to the real A6 matrix, with its own manual
opt-in. It also exercises hot-post warmups before a selective lookup. See the
[WHERE regression section](explain.md#focused-where-regression-gate). No new
infrastructure provisioning or GitHub Actions execution is added.

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

A1 proves healthy topology and WAL archival round-trip; the separate A2 test
proves application recovery after a primary-container crash. The separate A3
gate proves a full physical backup can be restored to a chosen timestamp.
The single-host lab has one proxy and one S3 service: it does not prove host-failure tolerance or end-to-end production HA.
No migration, public Go API, ordinary development Compose or GitHub Actions
workflow changes are made.

Sources: [Patroni replication policy](https://patroni.readthedocs.io/en/latest/replication_modes.html),
[Patroni REST API](https://patroni.readthedocs.io/en/latest/rest_api.html),
[Postgres archiving](https://www.postgresql.org/docs/16/continuous-archiving.html),
[Postgres recovery targets](https://www.postgresql.org/docs/16/runtime-config-wal.html#RUNTIME-CONFIG-WAL-RECOVERY-TARGET),
[WAL-G storage](https://wal-g.readthedocs.io/STORAGES/),
[SeaweedFS mini](https://github.com/seaweedfs/seaweedfs#quick-start).

### Ordenação por UUID

`make postgres-ha-id-sort-test POSTGRES_HA_PORT=5540` executa o gate manual
local de `_id`, sem provisionar serviços. Em schema isolado, o adapter real
consulta 20 mil UUIDs determinísticos em tabela com identificador escapado.
Valida ASC/DESC e páginas com offsets 0, 10 e 19990 nos modos auto, custom e
generic: 18 planos e conjuntos ordenados conferidos por IDs exatos. As páginas
iniciais exigem scan da PK na direção correta, sem Sort (12 verificações);
o offset alto verifica apenas resultados, pois a escolha de plano depende
do custo. Nenhum scan é desabilitado. SQL, planos, configurações e resultados
ficam em `.local/postgres-id-sort/`; schema e conexões são removidos ao final.
O gate requer `POSTGRES_ID_SORT_TEST=1` e `POSTGRES_EXPLAIN_URI` quando invocado
diretamente, e permanece fora do GitHub Actions. Não cobre ordenação tipada
JSONB, sparse nem mudanças na política de planos genéricos.

### Predicados sparse

`make postgres-ha-sparse-test POSTGRES_HA_PORT=5540` valida o lookup seletivo
no índice parcial com `$exists: true` explícito e Index Cond em auto/custom. Reutiliza os 30 planos A6 e
mantém o caso de chave ausente e generic como controles. Gate exclusivamente
manual/local; detalhes e limites em [explain.md](explain.md#sparse-predicate-regression-gate).
