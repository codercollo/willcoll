#!/usr/bin/env sh
# Pre-cutover backup for the platform database split (ADR-0006,
# build-phases.txt Phase 3, step 3a). Run this once, manually, immediately
# before 3b's platform migrations and 3d's backfill touch anything. It does
# two things, not one, because a backup that has never been restored is
# unverified:
#
#   1. pg_dump's the whole willcoll database (custom format) to a file.
#   2. Restores that dump into a THROWAWAY database and asserts every
#      table's row count matches the source exactly, then drops the
#      throwaway database. The dump file itself is left in place — that
#      file, not the throwaway database, is the actual backup to keep.
#
# This does NOT touch willcoll_platform, willcoll_rentals or any other
# database, and it makes no destructive change to willcoll: it only reads
# it. Nothing about steps 3b-3h requires this backup to have run first
# except good judgement; step 3i (dropping the platform-owned tables and
# the tenant_id FK from willcoll_rentals) MUST NOT run until this has been
# run and its restore-test has passed.
#
# Needs, from the environment or backend/.env:
#   MIGRATE_DSN   owner/superuser DSN of the willcoll database (its name
#                must be "willcoll"; the throwaway restore target is
#                derived from it, same convention as test_integration.sh)
# and `pg_dump`, `pg_restore`, `psql` on the PATH. DSNs are never printed.
#
# Usage: sh scripts/backup_before_split.sh [output-directory, default ./backups]
set -eu

cd "$(dirname "$0")/.."

# Same .env reader as test_integration.sh: anything already in the
# environment wins, and the file is read without being sourced (it also
# feeds `make`, and a value like SMTP_SENDER=Willcoll <a@b> isn't valid shell).
envval() {
    eval "current=\${$1:-}"
    if [ -n "$current" ]; then
        printf '%s' "$current"
    elif [ -f .env ]; then
        sed -n "s/^$1=//p" .env | head -n 1 | sed 's/^"\(.*\)"$/\1/'
    fi
}
MIGRATE_DSN=$(envval MIGRATE_DSN)
: "${MIGRATE_DSN:?MIGRATE_DSN is not set}"

OUT_DIR="${1:-./backups}"
mkdir -p "$OUT_DIR"

SCRATCH=willcoll_split_verify
swap_db() { echo "$1" | sed "s#/willcoll?#/${SCRATCH}?#"; }
SCRATCH_DSN=$(swap_db "$MIGRATE_DSN")
if [ "$SCRATCH_DSN" = "$MIGRATE_DSN" ]; then
    echo "backup_before_split: expected MIGRATE_DSN to name a database called willcoll" >&2
    exit 1
fi

cleanup() {
    psql "$MIGRATE_DSN" -qc "DROP DATABASE IF EXISTS ${SCRATCH}" >/dev/null 2>&1 || true
}
trap cleanup EXIT INT TERM

STAMP=$(date -u +%Y%m%dT%H%M%SZ)
DUMP_FILE="${OUT_DIR}/willcoll-pre-platform-split-${STAMP}.dump"

log() { printf '%s backup_before_split: %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$*"; }

log "dumping willcoll to ${DUMP_FILE}"
pg_dump "$MIGRATE_DSN" --format=custom --no-owner --no-privileges --file="$DUMP_FILE"

log "restoring the dump into throwaway database ${SCRATCH} to verify it"
psql "$MIGRATE_DSN" -qc "DROP DATABASE IF EXISTS ${SCRATCH}" >/dev/null
psql "$MIGRATE_DSN" -qc "CREATE DATABASE ${SCRATCH}" >/dev/null
pg_restore --dbname="$SCRATCH_DSN" --no-owner --no-privileges "$DUMP_FILE"

log "comparing row counts, table by table"
counts_query="
SELECT c.relname, c.reltuples::bigint
FROM pg_class c
JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE n.nspname = 'public' AND c.relkind = 'r'
ORDER BY c.relname"

# reltuples is a planner estimate, not exact, on a freshly restored (never
# analyzed) database — ANALYZE first so the comparison is exact, not
# approximate. A mismatch here means the dump or restore silently dropped
# rows, which is exactly what this verification exists to catch.
psql "$MIGRATE_DSN" -qc "ANALYZE" >/dev/null
psql "$SCRATCH_DSN" -qc "ANALYZE" >/dev/null

SOURCE_COUNTS=$(psql "$MIGRATE_DSN" -X -A -t -F'|' -c "$counts_query")
RESTORED_COUNTS=$(psql "$SCRATCH_DSN" -X -A -t -F'|' -c "$counts_query")

if [ "$SOURCE_COUNTS" != "$RESTORED_COUNTS" ]; then
    log "MISMATCH between source and restored row counts:"
    echo "--- source ---"
    echo "$SOURCE_COUNTS"
    echo "--- restored ---"
    echo "$RESTORED_COUNTS"
    log "the dump at ${DUMP_FILE} is UNVERIFIED — do not proceed with the platform split until this is understood"
    exit 1
fi

log "row counts match: the dump at ${DUMP_FILE} is verified restorable"
log "keep this file until Phase 3 step 3i has shipped and been confirmed in production"
