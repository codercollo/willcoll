#!/usr/bin/env sh
# Runs the whole test suite, including the Postgres integration and
# concurrency tests, against THROWAWAY databases. It never touches the
# development databases: it creates willcoll_verify (Rentals) and
# willcoll_platform_verify (platform) next to them, applies every migration of
# each, runs `go test ./...` with every integration DSN set, prints how many
# tests ran / passed / failed / skipped per package, and drops both databases
# again (also on failure or Ctrl-C).
#
# INTEGRATION=1 is exported: with it, an integration test whose DSN is missing
# FAILS instead of skipping (internal/testenv), so a green run here means the
# database tests really ran. The summary's skip column shows anything else.
#
# Packages run one at a time (-p 1): they all share the two scratch databases,
# so parallel packages contend on the same tables and CPU and make the
# concurrency tests flaky. Per-package databases would lift this; until then
# CI runs this same script, so it gets the same setting. Pass your own -p to
# override.
#
# Settings and the platform roles' passwords: see scripts/lib_testdb.sh.
# Needs `psql` on the PATH; uses golang-migrate (`migrate`) when installed,
# otherwise applies the .up.sql files with psql. DSNs are never printed.
#
# Usage: sh scripts/test_integration.sh [go test args, default ./...]
set -eu

cd "$(dirname "$0")/.."
. ./scripts/lib_testdb.sh
testdb_init

SCRATCH=willcoll_verify
SCRATCH_PLATFORM=willcoll_platform_verify

# shellcheck disable=SC2317,SC2329 # false positive: cleanup runs from the trap below
cleanup() {
    psql "$MIGRATE_DSN" -qc "DROP DATABASE IF EXISTS ${SCRATCH}" >/dev/null 2>&1 || true
    psql "$MIGRATE_DSN" -qc "DROP DATABASE IF EXISTS ${SCRATCH_PLATFORM}" >/dev/null 2>&1 || true
}
trap cleanup EXIT INT TERM

for db in "$SCRATCH" "$SCRATCH_PLATFORM"; do
    psql "$MIGRATE_DSN" -qc "DROP DATABASE IF EXISTS ${db}" >/dev/null
    psql "$MIGRATE_DSN" -qc "CREATE DATABASE ${db}"
done
migrate_up ./migrations/rentals "$(owner_dsn "$SCRATCH")"
migrate_up ./migrations/platform "$(owner_dsn "$SCRATCH_PLATFORM")"

if [ "$#" -eq 0 ]; then
    set -- ./...
fi

results="${TMPDIR:-/tmp}/willcoll_integration.json"
status=0
INTEGRATION=1 \
WILLCOLL_TEST_DB_DSN="$(app_dsn "$SCRATCH")" \
WILLCOLL_TEST_ADMIN_DB_DSN="$(admin_dsn "$SCRATCH")" \
WILLCOLL_TEST_PLATFORM_DB_DSN="$(platform_app_dsn "$SCRATCH_PLATFORM")" \
WILLCOLL_TEST_PLATFORM_ADMIN_DB_DSN="$(platform_admin_dsn "$SCRATCH_PLATFORM")" \
    go test -count=1 -p 1 -json "$@" >"$results" || status=$?

# The json stream is the source of truth; the summary prints the per-package
# table (run / pass / fail / skip) and, on failure, the failing output.
go run ./cmd/testsummary <"$results" || status=1
exit "$status"
