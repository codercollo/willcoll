#!/usr/bin/env sh
# One-time (and safe to re-run) setup of the local dev stack: the two
# databases, their roles, every migration, and a demo firm to log in with.
#
#   make dev/setup        (or: sh scripts/dev_setup.sh)
#
# What it does, in order. Every step checks first, so a re-run changes nothing
# that is already in place:
#   1. creates the cluster-wide roles willcoll_app, willcoll_admin,
#      willcoll_platform_app, willcoll_platform_admin when missing (an existing
#      role keeps its password; if the password dev knows does not work it stops
#      and says how to fix that);
#   2. creates the databases DEV_RENTALS_DB (default willcoll_rentals) and
#      DEV_PLATFORM_DB (default willcoll_platform) when missing;
#   3. runs every migration for both;
#   4. seeds the demo firm (cmd/seed) once, plus the TEST - KES 10 plan
#      (DEV_RESEED=yes replaces an existing or half-seeded demo firm);
#   5. claims every property's payment channel in platform.payment_channels and
#      verifies each has a matching claim (fixes an older dev database in place);
#   6. prints the demo logins.
#
# Your existing rentals database (the one DB_DSN names, normally "willcoll")
# is never connected to: see scripts/lib_dev.sh. Needs psql and go; uses
# `migrate` when installed, else psql.
set -eu
cd "$(dirname "$0")/.."
. ./scripts/lib_dev.sh

step() { printf '\n\033[1m== %s\033[0m\n' "$*"; }

dev_init

step "1. roles"
ensure_dev_role willcoll_app "$APP_PW" NOBYPASSRLS || { role_help willcoll_app DB_APP_PASSWORD; exit 1; }
ensure_dev_role willcoll_admin "$ADMIN_PW" BYPASSRLS || { role_help willcoll_admin DB_ADMIN_PASSWORD; exit 1; }
ensure_dev_role willcoll_platform_app "$PAPP_PW" NOBYPASSRLS || { role_help willcoll_platform_app PLATFORM_DB_APP_PASSWORD; exit 1; }
ensure_dev_role willcoll_platform_admin "$PADM_PW" BYPASSRLS || { role_help willcoll_platform_admin PLATFORM_DB_ADMIN_PASSWORD; exit 1; }

step "2. databases"
for db in "$RENTALS_DB" "$PLATFORM_DB"; do
    if [ "$(psql "$MIGRATE_DSN" -Atc "SELECT 1 FROM pg_database WHERE datname = '$db'")" = 1 ]; then
        echo "  $db: exists"
    else
        psql "$MIGRATE_DSN" -qc "CREATE DATABASE $db" && echo "  $db: created"
    fi
done

step "3. migrations"
migrate_up ./migrations/platform "$OWNER_PLATFORM" && echo "  $PLATFORM_DB: up to date"
migrate_up ./migrations/rentals "$OWNER_RENTALS" && echo "  $RENTALS_DB: up to date"

step "4. demo firm"
DEMO_EMAIL=demo@willcoll.test
have=$(psql "$OWNER_PLATFORM" -Atc "SELECT 1 FROM managers WHERE email = '$DEMO_EMAIL'")
# The seeder reads these names. ADMIN_* come from backend/.env.
(
    export ENV=development
    export DB_DSN="$RENTALS_APP" DB_ADMIN_DSN="$RENTALS_ADMIN" MIGRATE_DSN="$OWNER_RENTALS"
    # The seeder creates managers, subscriptions and invoices, which the narrow
    # willcoll_platform_admin role may not (migration 000013): it runs as the owner.
    export PLATFORM_DSN="$OWNER_PLATFORM" PLATFORM_MIGRATE_DSN="$OWNER_PLATFORM"
    # The callback URL recorded on each channel claim (same variable rentals-api uses).
    export PAYHERO_CALLBACK_BASE_URL="http://127.0.0.1:$RENTALS_PORT"
    ADMIN_NAME=$(envval ADMIN_NAME) ADMIN_EMAIL=$(envval ADMIN_EMAIL) ADMIN_PASSWORD=$(envval ADMIN_PASSWORD)
    export ADMIN_NAME ADMIN_EMAIL ADMIN_PASSWORD
    if [ "$have" = 1 ] && [ "$(envval DEV_RESEED)" != yes ]; then
        echo "  demo firm $DEMO_EMAIL already exists: not seeding again (DEV_RESEED=yes make dev/setup replaces it)"
    else
        reset=""
        [ "$have" = 1 ] && reset=-reset
        go run ./cmd/seed $reset || {
            echo "dev: the seeder stopped part-way. Fix the error above, then run: DEV_RESEED=yes make dev/setup" >&2
            exit 1
        }
    fi
    go run ./cmd/devtools test-plan
    # Every property's PayHero channel must be claimed in platform: the seeder
    # does it for new demo firms; this repairs an older dev database in place
    # (no reset) and then VERIFIES, failing with the channel and property named.
    go run ./cmd/devtools claim-channels || {
        echo "dev: a property's payment channel has no claim in $PLATFORM_DB.payment_channels (see above). Collections for it would fall back to local-only posting." >&2
        exit 1
    }
)

step "done"
cat <<EOF

Databases   $RENTALS_DB, $PLATFORM_DB
Start       make dev/up        (platform-api :$PLATFORM_PORT, rentals-api :$RENTALS_PORT, local SMS/PayHero stand-ins)
Frontend    cd ../frontend && pnpm dev      starts platform :3001 (sign in) and rentals :3000
            then open http://localhost:3001

Demo logins (sign in at http://localhost:3001/auth/login; you land back in Rentals)
  Manager       demo@willcoll.test  /  Demo-Password-1
EOF
if [ -n "$(envval ADMIN_EMAIL)" ] && [ -n "$(envval ADMIN_PASSWORD)" ]; then
    echo "  Super Admin   $(envval ADMIN_EMAIL)  /  the ADMIN_PASSWORD in backend/.env   (http://localhost:3001/admin/login)"
else
    echo "  Super Admin   not provisioned: set ADMIN_EMAIL and ADMIN_PASSWORD (12+ chars) in backend/.env and re-run"
fi
