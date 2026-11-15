#!/bin/sh
set -eu
# ha-entrypoint has already prepared this sandbox's anonymous PGDATA and
# dropped to postgres. Fail rather than reuse an existing restore directory.
: "${PGDATA:?}" "${PITR_BACKUP:?}" "${PITR_TARGET_TIME:?}" "${PITR_TIMELINE:?}"
case "$PITR_BACKUP" in base_*) ;; *) echo 'explicit base backup required' >&2; exit 1 ;; esac
case "$PITR_BACKUP" in *[!a-zA-Z0-9_]*) exit 1 ;; esac
case "$PITR_TARGET_TIME" in *[!0-9:.+\ -]*|'') exit 1 ;; esac
case "$PITR_TIMELINE" in *[!0-9]*|''|0) exit 1 ;; esac
if [ -n "$(ls -A "$PGDATA")" ]; then
    echo 'restore requires empty sandbox PGDATA' >&2
    exit 1
fi
wal-g backup-fetch "$PGDATA" "$PITR_BACKUP"
# Never inherit Patroni's replication/archive configuration. The copy can only
# read WAL from S3; after promotion it must not archive its divergent history.
rm -f "$PGDATA/standby.signal" "$PGDATA/postmaster.pid"
: > "$PGDATA/postgresql.auto.conf"
: > "$PGDATA/pg_ident.conf"
cat > "$PGDATA/pg_hba.conf" <<'HBA'
local all all trust
host postgres postgres 0.0.0.0/0 scram-sha-256
HBA
cat > "$PGDATA/postgresql.conf" <<'CONF'
listen_addresses = '*'
port = 5432
unix_socket_directories = '/var/run/postgresql'
hot_standby = on
wal_level = replica
max_connections = 50
max_wal_senders = 10
shared_buffers = '64MB'
archive_mode = off
archive_command = ''
synchronous_standby_names = ''
synchronous_commit = on
primary_conninfo = ''
primary_slot_name = ''
restore_command = 'wal-g wal-fetch "%f" "%p"'
recovery_target_action = 'pause'
recovery_target_inclusive = on
CONF
printf "recovery_target_time = '%s'\nrecovery_target_timeline = '%s'\n" \
    "$PITR_TARGET_TIME" "$PITR_TIMELINE" >> "$PGDATA/postgresql.conf"
touch "$PGDATA/recovery.signal"
exec postgres -D "$PGDATA"
