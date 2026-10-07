#!/usr/bin/env sh
# Rehearses the whole platform-split cutover, from nothing, on this machine
# (build-phases.txt 3i-c). It stands in for a staging rehearsal until Phase 9's
# VPS exists, and Phase 9 runs it for real before the first production cutover.
# Keep it working as later phases add migrations.
#
# From a fresh PRE-SPLIT database it proves, in order:
#   1. the old Rentals migrations (1-44) apply, and a pre-split customer is
#      seeded into it (manager + password, subscription, a property with a
#      hand-typed PayHero channel id);
#   2. willcoll_platform's migrations apply;
#   3. `devtools backfill-platform` copies that customer's identity and billing
#      into willcoll_platform, seeding manager_modules and module_usage and
#      claiming the property's hand-typed channel in payment_channels;
#   4. migration 000045 drops the identity tables from the Rentals database;
#   5. the database is renamed (willcoll -> willcoll_rentals, as on the VPS);
#   6. the REAL platform-api and rentals-api binaries start against the result
#      (with local fakes standing in for Africa's Talking and PayHero);
#   7. an HTTP smoke test (cmd/rehearse): the backfilled customer signs in on
#      platform-api and is served by rentals-api; a new firm signs up, activates
#      and signs in on platform-api; creates a property and unit on rentals-api;
#      runs rent; a tenant pays from the public page (OTP, push, webhook) and a
#      replayed webhook posts once; a hand-typed channel id is claimed at
#      save time (409 if taken, released when changed) and its webhook goes
#      through the platform payment inbox, not the local fallback; staff are role-gated; the Super Admin
#      oversees; suspending the firm gets its manager AND staff a 401 on BOTH
#      APIs, and reinstating restores them.
# With --with-gate it first runs the full gate (go build, go vet, go test,
# make test/integration), so one command proves build, tests and cutover.
#
# Every database exists only for this run (willcoll_rehearse -> renamed
# willcoll_rentals_rehearse, and willcoll_platform_rehearse) and is dropped at
# the end, also on failure or Ctrl-C. The dev databases are never touched. The
# platform roles are cluster-wide; see scripts/lib_testdb.sh. Needs psql, go and
# the same .env as test_integration.sh. Ports 14000/14100/14901/14902 must be free
# (override: RENTALS_PORT, PLATFORM_PORT).
#
# Usage: sh scripts/rehearse_cutover.sh [--with-gate] [--keep]
#   --keep leaves the three databases and the logs in place (drop them with
#   DROP DATABASE ... when done) so a failure can be inspected.
set -eu

cd "$(dirname "$0")/.."
. ./scripts/lib_testdb.sh

with_gate=0
keep=0
for arg in "$@"; do
    case "$arg" in
    --with-gate) with_gate=1 ;;
    --keep) keep=1 ;; # leave the databases (and logs) behind for debugging
    *)
        echo "usage: sh scripts/rehearse_cutover.sh [--with-gate] [--keep]" >&2
        exit 2
        ;;
    esac
done

step() { printf '\n\033[1m== %s\033[0m\n' "$*"; }

if [ "$with_gate" -eq 1 ]; then
    step "gate: go build, go vet, go test"
    go build ./... && go vet ./... && go test -count=1 ./...
    step "gate: integration tests (both scratch databases)"
    INTEGRATION=1 sh ./scripts/test_integration.sh
fi

testdb_init

OLD=willcoll_rehearse
FINAL=willcoll_rentals_rehearse
PLAT=willcoll_platform_rehearse
RENTALS_PORT=${RENTALS_PORT:-14000}
PLATFORM_PORT=${PLATFORM_PORT:-14100}
WORK=$(mktemp -d "${TMPDIR:-/tmp}/rehearse.XXXXXX")
PIDS=""

drop_all() {
    for db in "$OLD" "$FINAL" "$PLAT"; do
        psql "$MIGRATE_DSN" -qc "DROP DATABASE IF EXISTS ${db} WITH (FORCE)" >/dev/null 2>&1 || true
    done
}
stop_all() {
    for pid in $PIDS; do kill "$pid" 2>/dev/null || true; done
    PIDS=""
}
finish() {
    code=$?
    stop_all
    [ "$keep" -eq 1 ] || drop_all
    if [ "$code" -ne 0 ] || [ "$keep" -eq 1 ]; then
        echo "rehearsal exit $code; logs kept in $WORK" >&2
        for f in "$WORK"/*.log; do
            [ -f "$f" ] && { echo "--- tail of $(basename "$f")" >&2; tail -n 15 "$f" >&2; }
        done
    else
        rm -rf "$WORK"
    fi
}
trap finish EXIT INT TERM
drop_all

# ------------------------------------------------------------------ 1. old DB
step "1. fresh pre-split database: migrations 1-44, then a pre-split customer"
psql "$MIGRATE_DSN" -qc "CREATE DATABASE ${OLD}"
migrate_up ./migrations/rentals "$(owner_dsn "$OLD")" 44

OLD_EMAIL=oldfirm@rehearsal.example.com
OLD_PASSWORD=Old-Password-1
OLD_HASH=$(go run ./cmd/rehearse hash "$OLD_PASSWORD")
psql "$(owner_dsn "$OLD")" -q -v ON_ERROR_STOP=1 -v hash="$OLD_HASH" -v email="$OLD_EMAIL" <<'SQL'
WITH m AS (
    INSERT INTO managers (firm_name, username, email, phone, password_hash, status, activated_at, trial_ends_at)
    VALUES ('Old Firm', 'oldfirm', :'email', '+254700000001', convert_to(:'hash', 'UTF8'), 'active', now() - interval '60 days', now() - interval '53 days')
    RETURNING id
), p AS (
    INSERT INTO subscription_plans (name, price, billing_interval, unit_cap)
    VALUES ('Old Plan', 3500, 'monthly', 60) RETURNING id
), s AS (
    INSERT INTO subscriptions (manager_id, plan_id, status, current_period_start, current_period_end)
    SELECT m.id, p.id, 'active', current_date - 5, current_date + 25 FROM m, p RETURNING manager_id
), l AS (
    INSERT INTO landlords (tenant_id, name, phone)
    SELECT id, 'Old Landlord', '+254711000000' FROM m RETURNING id, tenant_id
)
INSERT INTO properties (tenant_id, landlord_id, name, location, slug, payhero_channel_id)
SELECT tenant_id, id, 'Old Court', 'Nairobi', 'old-court', 'OLD-HANDTYPED' FROM l;
SQL
echo "seeded a pre-split manager, plan, subscription and property (with a hand-typed PayHero channel id)"

# --------------------------------------------------------- 2-3. platform + backfill
step "2. willcoll_platform: create and migrate"
psql "$MIGRATE_DSN" -qc "CREATE DATABASE ${PLAT}"
migrate_up ./migrations/platform "$(owner_dsn "$PLAT")"

step "3. backfill-platform (old identity and billing -> willcoll_platform)"
# PLATFORM_DSN is willcoll_platform's OWNER: willcoll_platform_admin only has
# SELECT/UPDATE (migration 000013), so it cannot insert the copied rows.
# PAYHERO_CALLBACK_BASE_URL is required for the payment_channels pass (3i-d): the
# backfill skips channels without it.
SOURCE_DSN="$(admin_dsn "$OLD")" PLATFORM_DSN="$(owner_dsn "$PLAT")" \
    PAYHERO_CALLBACK_BASE_URL="http://127.0.0.1:$RENTALS_PORT" go run ./cmd/devtools backfill-platform
claimed=$(psql "$(owner_dsn "$PLAT")" -Atc "SELECT product FROM payment_channels WHERE channel_id = 'OLD-HANDTYPED'")
[ "$claimed" = "rentals" ] || { echo "the pre-split hand-typed channel was not claimed in willcoll_platform by the backfill" >&2; exit 1; }
echo "the pre-split property's hand-typed channel id is claimed in willcoll_platform"

# ------------------------------------------------------------------ 4-5. drop + rename
step "4. migration 000045: drop the local identity tables"
migrate_up ./migrations/rentals "$(owner_dsn "$OLD")"
left=$(psql "$(owner_dsn "$OLD")" -Atc "SELECT count(*) FROM information_schema.tables WHERE table_schema='public' AND table_name IN ('managers','admins','tokens','subscriptions','subscription_plans','subscription_invoices')")
[ "$left" = "0" ] || { echo "identity tables survived 000045 ($left left)" >&2; exit 1; }
echo "identity tables dropped; the Rentals data is intact:"
psql "$(owner_dsn "$OLD")" -Atc "SELECT '  properties: ' || count(*) FROM properties"

step "5. rename $OLD -> $FINAL (as willcoll -> willcoll_rentals on the VPS)"
psql "$MIGRATE_DSN" -qc "ALTER DATABASE ${OLD} RENAME TO ${FINAL}"

# ------------------------------------------------------------------ 6. start
step "6. build and start platform-api + rentals-api (with fake SMS / PayHero)"
go build -o "$WORK/platform-api" ./cmd/platform-api
go build -o "$WORK/rentals-api" ./cmd/rentals-api
go build -o "$WORK/rehearse" ./cmd/rehearse

WEBHOOK_SECRET=rehearsal-webhook-secret
ADMIN_EMAIL=admin@rehearsal.example.com
ADMIN_PASSWORD=Rehearsal-Admin-Pass-1

# Compute every DSN BEFORE any command line below: the helpers derive from
# DB_DSN, and an inline `DB_DSN=... cmd` assignment would change it under the
# helpers still to be expanded on that same line.
RENTALS_APP=$(app_dsn "$FINAL")
RENTALS_ADMIN=$(admin_dsn "$FINAL")
PLATFORM_APP=$(platform_app_dsn "$PLAT")
PLATFORM_ADMIN=$(platform_admin_dsn "$PLAT")

export ENV=development LIMITER_ENABLED=false
# The smoke test reads activation/invite tokens out of platform-api's log,
# which only happens with the development LogMailer. A real SMTP_HOST in the
# developer's .env would send them to a mail server instead (and the step
# would fail), so blank it for this run.
export SMTP_HOST=
export PAYHERO_API_KEY=rehearsal PAYHERO_WEBHOOK_SECRET="$WEBHOOK_SECRET"

"$WORK/rehearse" fakes >"$WORK/fakes.log" 2>&1 &
PIDS="$PIDS $!"

PORT=$PLATFORM_PORT \
DB_DSN="$PLATFORM_APP" DB_ADMIN_DSN="$PLATFORM_ADMIN" \
ADMIN_NAME="Rehearsal Admin" ADMIN_EMAIL="$ADMIN_EMAIL" ADMIN_PASSWORD="$ADMIN_PASSWORD" \
PAYHERO_BASE_URL=http://127.0.0.1:14902 PAYHERO_BILLING_CHANNEL_ID=BILLING-REHEARSAL \
PAYHERO_CALLBACK_BASE_URL="http://127.0.0.1:$PLATFORM_PORT" \
RENTALS_API_URL="http://127.0.0.1:$RENTALS_PORT" HEALTH_TIMEOUT=10s DB_QUERY_TIMEOUT=10s \
    "$WORK/platform-api" >"$WORK/platform-api.log" 2>&1 &
PIDS="$PIDS $!"

PORT=$RENTALS_PORT \
DB_DSN="$RENTALS_APP" DB_ADMIN_DSN="$RENTALS_ADMIN" \
PLATFORM_DB_DSN="$PLATFORM_APP" PLATFORM_DB_ADMIN_DSN="$PLATFORM_ADMIN" \
DB_QUERY_TIMEOUT=10s PAYHERO_BASE_URL=http://127.0.0.1:14902 PAYHERO_COLLECTIONS_CHANNEL_ID=COLLECTIONS-REHEARSAL PAYHERO_ACCOUNT_ID=1 \
PAYHERO_CALLBACK_BASE_URL="http://127.0.0.1:$RENTALS_PORT" \
PAY_SESSION_SECRET=rehearsal-pay-session-secret-0123456789 QR_BASE_URL=http://127.0.0.1:3000 \
AFRICASTALKING_API_KEY=rehearsal AFRICASTALKING_USERNAME=rehearsal AFRICASTALKING_BASE_URL=http://127.0.0.1:14901 \
    "$WORK/rentals-api" >"$WORK/rentals-api.log" 2>&1 &
PIDS="$PIDS $!"

wait_for() { # url name
    i=0
    while [ $i -lt 60 ]; do
        if curl -fsS "$1" >/dev/null 2>&1; then echo "$2 is up"; return 0; fi
        i=$((i + 1))
        sleep 0.5
    done
    echo "$2 did not come up at $1" >&2
    return 1
}
wait_for "http://127.0.0.1:$PLATFORM_PORT/v1/healthcheck" platform-api
wait_for "http://127.0.0.1:$RENTALS_PORT/v1/healthcheck" rentals-api

# ------------------------------------------------------------------ 7. smoke
step "7. HTTP smoke test"
PLATFORM_URL="http://127.0.0.1:$PLATFORM_PORT" RENTALS_URL="http://127.0.0.1:$RENTALS_PORT" \
FAKE_SMS_URL=http://127.0.0.1:14901 FAKE_PAYHERO_URL=http://127.0.0.1:14902 \
WEBHOOK_SECRET="$WEBHOOK_SECRET" PLATFORM_LOG="$WORK/platform-api.log" \
RENTALS_ADMIN_DSN="$RENTALS_ADMIN" PLATFORM_ADMIN_DSN="$PLATFORM_ADMIN" \
OLD_FIRM_EMAIL="$OLD_EMAIL" OLD_FIRM_PASSWORD="$OLD_PASSWORD" \
ADMIN_EMAIL="$ADMIN_EMAIL" ADMIN_PASSWORD="$ADMIN_PASSWORD" \
    "$WORK/rehearse" smoke

step "cutover rehearsal passed"
