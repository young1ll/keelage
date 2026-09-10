#!/usr/bin/env bash
# Throwaway PostgreSQL 16 cluster for the server ledger tests.
#   scripts/pg-test.sh start   → prints the DSN; export KEELAGE_TEST_PG=<dsn>
#   scripts/pg-test.sh stop
# Runs as the `postgres` user when invoked by root (postgres refuses root).
set -euo pipefail
DIR="${KEELAGE_PG_DIR:-/tmp/keelage-pg}"
PORT="${KEELAGE_PG_PORT:-54329}"
BIN="${PG_BIN:-$(ls -d /usr/lib/postgresql/*/bin 2>/dev/null | sort -V | tail -1)}"
[ -n "$BIN" ] || { echo "postgres binaries not found; install postgresql-16" >&2; exit 1; }
run() { if [ "$(id -u)" = 0 ]; then su -s /bin/sh postgres -c "$*"; else sh -c "$*"; fi; }
case "${1:-}" in
  start)
    mkdir -p "$DIR"; if [ "$(id -u)" = 0 ]; then chown postgres:postgres "$DIR"; fi; chmod 755 "$DIR"
    [ -d "$DIR/data" ] || run "$BIN/initdb -D $DIR/data -U keelage --auth=trust -E UTF8 >$DIR/initdb.log 2>&1"
    run "$BIN/pg_ctl -D $DIR/data -o '-p $PORT -k $DIR -c listen_addresses=' -l $DIR/pg.log -w start >/dev/null"
    psql -h "$DIR" -p "$PORT" -U keelage -d postgres -qc 'CREATE DATABASE keelage_test' 2>/dev/null || true
    echo "postgres://keelage@/keelage_test?host=$DIR&port=$PORT&sslmode=disable"
    ;;
  stop) run "$BIN/pg_ctl -D $DIR/data -m fast stop >/dev/null" ;;
  *) echo "usage: $0 start|stop" >&2; exit 2 ;;
esac
