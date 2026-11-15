#!/bin/sh
set -eu
# Patroni owns initdb and PostgreSQL. Do not invoke the image's initdb entrypoint.
if [ "$(id -u)" = 0 ]; then
    mkdir -p "$PGDATA" /var/run/postgresql
    chown postgres:postgres "$PGDATA" /var/run/postgresql
    chmod 0700 "$PGDATA"
    exec gosu postgres "$0" "$@"
fi
exec "$@"
