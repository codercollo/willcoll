#!/usr/bin/env sh
# Bounded retention (spec Phase 5.2) — deletes dumps older than
# BACKUP_RETENTION_DAYS (default 7) so nightly-pg-dump.sh can never fill the
# VPS disk unattended.
set -eu

BACKUP_DIR="${BACKUP_DIR:-/backups}"
RETENTION_DAYS="${BACKUP_RETENTION_DAYS:-7}"

find "$BACKUP_DIR" -maxdepth 1 -name 'willcoll-*.sql.gz*' -mtime "+$RETENTION_DAYS" -print -delete
