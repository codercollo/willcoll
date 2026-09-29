#!/usr/bin/env bash
# Backup verification (system-design.txt 8.3). Run from cron on the
# database host, e.g. hourly:
#
#   0 * * * *  /opt/willcoll/scripts/db_backup_verify.sh >> /var/log/willcoll-backup.log 2>&1
#
# It does two things:
#   1. Reads the newest base backup (completion time and size) from the
#      backup repository, which, unlike Postgres, knows the size, and
#      records it in system_metadata['last_backup'] (migration 000022).
#      GET /v1/admin/backups and /v1/admin/system/health read it from there.
#   2. Checks continuous WAL archiving (pg_stat_archiver) and exits non-zero
#      if it's failing or stale, so cron / monitoring can alert.
#
# Configuration (environment):
#   BACKUP_DB_DSN     Postgres DSN for the willcoll_admin role (the only role
#                     allowed to write system_metadata). Required.
#   BACKUP_TOOL       pgbackrest | s3 | manual   (default: pgbackrest)
#   PGBACKREST_STANZA stanza name, for BACKUP_TOOL=pgbackrest
#   BACKUP_S3_URI     s3://bucket/prefix/ holding the latest base backup,
#                     for BACKUP_TOOL=s3 (wal-g style). AWS credentials and,
#                     for S3-compatible storage, AWS_ENDPOINT_URL come from
#                     the usual AWS environment variables.
#   BACKUP_COMPLETED_AT, BACKUP_SIZE_BYTES, BACKUP_LABEL
#                     the backup to record, for BACKUP_TOOL=manual (other
#                     tools, or testing).
#   WAL_MAX_AGE_SECONDS  how old the last archived WAL may be (default 86400).
#
# Requires: psql, jq; plus pgbackrest or aws for those modes.

set -euo pipefail

: "${BACKUP_DB_DSN:?BACKUP_DB_DSN must be set to the willcoll_admin DSN}"
BACKUP_TOOL="${BACKUP_TOOL:-pgbackrest}"
WAL_MAX_AGE_SECONDS="${WAL_MAX_AGE_SECONDS:-86400}"

log() { printf '%s db_backup_verify: %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$*"; }

# latest_from_pgbackrest prints "completed_at<TAB>size_bytes<TAB>label" for
# the stanza's newest backup.
latest_from_pgbackrest() {
	: "${PGBACKREST_STANZA:?PGBACKREST_STANZA must be set for BACKUP_TOOL=pgbackrest}"
	pgbackrest --stanza="$PGBACKREST_STANZA" --output=json info | jq -r '
		.[0].backup | if length == 0 then error("no backups in the repository") else . end
		| max_by(.timestamp.stop)
		| [(.timestamp.stop | todate), .info.repository.size, .label] | @tsv'
}

# latest_from_s3 prints "completed_at<TAB>size_bytes<TAB>label": the total
# size under BACKUP_S3_URI and the time of its newest object.
latest_from_s3() {
	: "${BACKUP_S3_URI:?BACKUP_S3_URI must be set for BACKUP_TOOL=s3}"
	aws s3 ls "$BACKUP_S3_URI" --recursive --summarize | parse_s3_listing "$BACKUP_S3_URI"
}

# parse_s3_listing reads `aws s3 ls --recursive --summarize` output: object
# lines are "YYYY-MM-DD HH:MM:SS <size> <key>", and a "Total Size: N" line
# ends the listing.
parse_s3_listing() {
	awk -v label="$1" '
		/^[0-9]{4}-[0-9]{2}-[0-9]{2} / { ts = $1 "T" $2 "Z"; if (ts > newest) newest = ts }
		/Total Size:/ { total = $3 }
		END {
			if (newest == "" || total == "") { print "no objects under " label > "/dev/stderr"; exit 1 }
			printf "%s\t%s\t%s\n", newest, total, label
		}'
}

latest_from_manual() {
	: "${BACKUP_COMPLETED_AT:?BACKUP_COMPLETED_AT must be set for BACKUP_TOOL=manual}"
	: "${BACKUP_SIZE_BYTES:?BACKUP_SIZE_BYTES must be set for BACKUP_TOOL=manual}"
	printf '%s\t%s\t%s\n' "$BACKUP_COMPLETED_AT" "$BACKUP_SIZE_BYTES" "${BACKUP_LABEL:-}"
}

case "$BACKUP_TOOL" in
	pgbackrest) latest="$(latest_from_pgbackrest)" ;;
	s3) latest="$(latest_from_s3)" ;;
	manual) latest="$(latest_from_manual)" ;;
	*) log "unknown BACKUP_TOOL '$BACKUP_TOOL' (want pgbackrest, s3 or manual)"; exit 2 ;;
esac

IFS=$'\t' read -r completed_at size_bytes label <<<"$latest"

# 1. Record the newest base backup for the admin dashboard.
record="$(jq -cn \
	--arg completed_at "$completed_at" \
	--argjson size_bytes "$size_bytes" \
	--arg tool "$BACKUP_TOOL" \
	--arg label "$label" \
	--arg verified_at "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
	'{completed_at: $completed_at, size_bytes: $size_bytes, tool: $tool, label: $label, verified_at: $verified_at}')"

psql "$BACKUP_DB_DSN" -X -q -v ON_ERROR_STOP=1 -v record="$record" <<'SQL'
INSERT INTO system_metadata (key, value, updated_at)
VALUES ('last_backup', :'record'::jsonb, now())
ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now();
SQL

log "recorded last base backup: completed_at=$completed_at size_bytes=$size_bytes tool=$BACKUP_TOOL"

# 2. Check continuous WAL archiving.
wal="$(psql "$BACKUP_DB_DSN" -X -A -t -v ON_ERROR_STOP=1 -c "
	SELECT archived_count,
	       COALESCE(EXTRACT(EPOCH FROM now() - last_archived_time)::bigint, -1),
	       (last_failed_time IS NOT NULL AND (last_archived_time IS NULL OR last_failed_time > last_archived_time))
	FROM pg_stat_archiver")"
IFS='|' read -r archived_count archive_age failing <<<"$wal"

if [[ "$failing" == "t" ]]; then
	log "WAL archiving is FAILING: the most recent archive attempt failed"
	exit 1
fi
if [[ "$archived_count" == "0" ]]; then
	log "WAL archiving has never archived a segment (archive_mode off?)"
	exit 1
fi
if (( archive_age > WAL_MAX_AGE_SECONDS )); then
	log "WAL archiving is STALE: last segment archived ${archive_age}s ago"
	exit 1
fi

log "WAL archiving healthy: last segment archived ${archive_age}s ago"
