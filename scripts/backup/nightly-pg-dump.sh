#!/usr/bin/env sh
# Runs inside the pg_backup container on a nightly cron schedule (spec §7).
# pg_dump -> gzip -> timestamped file in /backups. Optional GPG encryption
# and offsite push, both no-ops if their env vars are unset.
set -eu

BACKUP_DIR="${BACKUP_DIR:-/backups}"
STAMP="$(date -u +%Y%m%dT%H%M%SZ)"
DUMP_FILE="$BACKUP_DIR/willcoll-$STAMP.sql.gz"

mkdir -p "$BACKUP_DIR"

pg_dump "postgres://${POSTGRES_USER}:${POSTGRES_PASSWORD}@${POSTGRES_HOST:-postgres}:5432/${POSTGRES_DB}" \
  | gzip > "$DUMP_FILE"

if [ -n "${BACKUP_GPG_PASSPHRASE:-}" ]; then
  gpg --batch --yes --passphrase "$BACKUP_GPG_PASSPHRASE" -c "$DUMP_FILE"
  rm -f "$DUMP_FILE"
  DUMP_FILE="$DUMP_FILE.gpg"
fi

if [ -n "${BACKUP_OFFSITE_URL:-}" ]; then
  curl -fsS -T "$DUMP_FILE" "$BACKUP_OFFSITE_URL"
fi

echo "nightly-pg-dump.sh: wrote $DUMP_FILE"
