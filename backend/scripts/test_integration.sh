#!/usr/bin/env sh
# Runs the whole test suite, including the Postgres integration and
# concurrency tests, against a THROWAWAY database. It never touches the
# development database: it creates willcoll_verify next to it, applies every
# migration, runs `go test ./...` with the integration DSNs set, and drops the
# database again (also on failure or Ctrl-C).
#
# Needs, from the environment or backend/.env:
#   MIGRATE_DSN        owner/superuser DSN of the dev database (its name must
#                      be "willcoll"; the scratch database is derived from it)
#   DB_DSN             the API DSN (role willcoll_app)
#   DB_ADMIN_PASSWORD  password of the willcoll_admin role
# and `psql` on the PATH. Uses golang-migrate (`migrate`) when installed,
# otherwise applies the .up.sql files with psql. DSNs are never printed.
#
# Usage: sh scripts/test_integration.sh [go test args, default ./...]
set -eu

cd "$(dirname "$0")/.."

# Read only what is needed from .env, without sourcing it: the file is also
# consumed by make, and a value like SMTP_SENDER=Willcoll <a@b> is not valid
# shell. Anything already in the environment wins.
envval() {
    eval "current=\${$1:-}"
    if [ -n "$current" ]; then
        printf '%s' "$current"
    elif [ -f .env ]; then
        sed -n "s/^$1=//p" .env | head -n 1 | sed 's/^"\(.*\)"$/\1/'
    fi
}
MIGRATE_DSN=$(envval MIGRATE_DSN)
DB_DSN=$(envval DB_DSN)
DB_ADMIN_PASSWORD=$(envval DB_ADMIN_PASSWORD)

: "${MIGRATE_DSN:?MIGRATE_DSN is not set}"
: "${DB_DSN:?DB_DSN is not set}"
: "${DB_ADMIN_PASSWORD:?DB_ADMIN_PASSWORD is not set}"

SCRATCH=willcoll_verify
swap_db() { echo "$1" | sed "s#/willcoll?#/${SCRATCH}?#"; }

SCRATCH_MIGRATE=$(swap_db "$MIGRATE_DSN")
SCRATCH_APP=$(swap_db "$DB_DSN")
if [ "$SCRATCH_MIGRATE" = "$MIGRATE_DSN" ] || [ "$SCRATCH_APP" = "$DB_DSN" ]; then
    echo "test_integration: expected the DSNs to name a database called willcoll" >&2
    exit 1
fi
SCRATCH_ADMIN=$(echo "$SCRATCH_APP" | sed "s#//willcoll_app:[^@]*@#//willcoll_admin:${DB_ADMIN_PASSWORD}@#")

cleanup() {
    psql "$MIGRATE_DSN" -qc "DROP DATABASE IF EXISTS ${SCRATCH}" >/dev/null 2>&1 || true
}
trap cleanup EXIT INT TERM

psql "$MIGRATE_DSN" -qc "DROP DATABASE IF EXISTS ${SCRATCH}" >/dev/null
psql "$MIGRATE_DSN" -qc "CREATE DATABASE ${SCRATCH}"

if command -v migrate >/dev/null 2>&1; then
    migrate -path ./migrations -database "$SCRATCH_MIGRATE" up
else
    # golang-migrate keeps a schema_migrations table that migration 000018
    # reads; without the tool, create a stand-in and apply the files in order.
    psql "$SCRATCH_MIGRATE" -q -c "CREATE TABLE IF NOT EXISTS schema_migrations (version bigint PRIMARY KEY, dirty boolean NOT NULL)"
    for f in $(ls migrations/*.up.sql | sort); do
        psql "$SCRATCH_MIGRATE" -q -v ON_ERROR_STOP=1 -f "$f" >/dev/null
    done
fi

if [ "$#" -eq 0 ]; then
    set -- ./...
fi
WILLCOLL_TEST_DB_DSN="$SCRATCH_APP" WILLCOLL_TEST_ADMIN_DB_DSN="$SCRATCH_ADMIN" go test -count=1 "$@"
