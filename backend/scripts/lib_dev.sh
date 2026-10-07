#!/usr/bin/env sh
# Shared by scripts/dev_setup.sh, dev_up.sh and dev_run.sh (the local dev
# stack: platform-api + rentals-api + their two databases). SOURCE it from the
# backend directory. It defines functions and variables only.
#
# What it reads, from the environment or backend/.env (environment wins):
#   MIGRATE_DSN        a superuser DSN on the local cluster. Only its host,
#                      port, user and password are used: the database name in
#                      it is ignored, so an existing "willcoll" there is fine.
#   DB_APP_PASSWORD    willcoll_app's password (else read from DB_DSN's userinfo).
#   DB_ADMIN_PASSWORD  willcoll_admin's password.
#   PLATFORM_DB_APP_PASSWORD, PLATFORM_DB_ADMIN_PASSWORD   optional; see
#                      scripts/lib_testdb.sh (same roles, same throwaway
#                      fallback passwords, so the integration tests agree).
#   DEV_RENTALS_DB     default willcoll_rentals
#   DEV_PLATFORM_DB    default willcoll_platform
#   RENTALS_PORT (default: PORT, else 4000), PLATFORM_PORT (default 4100),
#   FAKE_SMS_ADDR / FAKE_PAYHERO_ADDR, DEV_FAKES (default true).
#
# The dev stack NEVER uses DB_DSN / PLATFORM_DB_DSN as written in .env: it
# derives its own DSNs for the DEV_* databases, so the database your existing
# DB_DSN points at is never migrated, seeded or even connected to.
#
# Nothing here prints a DSN or a password.

. ./scripts/lib_testdb.sh

dev_die() {
    echo "dev: $*" >&2
    exit 1
}

# URL helpers: postgres://user:pass@host:port/db?options
swap_db() { echo "$1" | sed "s#\(://[^/]*\)/[^?]*#\1/$2#"; }
swap_user() { echo "$1" | sed "s#^\([a-z]*://\)[^@]*@#\1$2:$3@#"; }
dsn_password() { echo "$1" | sed 's#^[a-z]*://[^:]*:\([^@]*\)@.*#\1#'; }

role_dsn() { swap_db "$(swap_user "$MIGRATE_DSN" "$2" "$3")" "$1"; } # <db> <role> <password>

can_login() { psql "$(role_dsn postgres "$1" "$2")" -Atc "SELECT 1" >/dev/null 2>&1; }

# ensure_dev_role <name> <password> <BYPASSRLS|NOBYPASSRLS>
# Creates the role when missing; never rewrites an existing role's password.
ensure_dev_role() {
    name=$1 password=$2 bypass=$3
    if role_exists "$name"; then
        if can_login "$name" "$password"; then
            echo "  role $name: exists, password works"
            return 0
        fi
        return 1
    fi
    psql "$MIGRATE_DSN" -qc "CREATE ROLE $name LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE $bypass PASSWORD '$password'" >/dev/null
    echo "  role $name: created"
}

role_help() { # <role> <the variable you can set>
    cat >&2 <<EOF
dev: role $1 exists but the password dev knows for it does not work.
     Either put its real password in backend/.env as $2=...
     or reset it to one you choose:
         psql "\$MIGRATE_DSN" -c "ALTER ROLE $1 PASSWORD 'choose-one'"
     and set $2=choose-one in backend/.env.
EOF
}

# dev_init: read the settings and compute every DSN. Exits on a bad setup.
dev_init() {
    MIGRATE_DSN=$(envval MIGRATE_DSN)
    [ -n "$MIGRATE_DSN" ] || dev_die "MIGRATE_DSN is not set (a superuser DSN on your local Postgres; see backend/.env.example)"
    psql "$MIGRATE_DSN" -Atc "SELECT 1" >/dev/null 2>&1 || dev_die "cannot connect with MIGRATE_DSN: is Postgres running, and are the user and password right?"

    RENTALS_DB=$(envval DEV_RENTALS_DB)
    RENTALS_DB=${RENTALS_DB:-willcoll_rentals}
    PLATFORM_DB=$(envval DEV_PLATFORM_DB)
    PLATFORM_DB=${PLATFORM_DB:-willcoll_platform}
    case "$RENTALS_DB$PLATFORM_DB" in
    *[!a-z0-9_]*) dev_die "DEV_RENTALS_DB / DEV_PLATFORM_DB may only contain a-z, 0-9 and _" ;;
    esac
    [ "$RENTALS_DB" != "$PLATFORM_DB" ] || dev_die "DEV_RENTALS_DB and DEV_PLATFORM_DB must differ"

    # Your existing data lives in the database DB_DSN names (normally
    # "willcoll"). The dev stack migrates and seeds its databases, and
    # migration 000045 DROPS the identity tables, so it must never be pointed
    # at that one without an explicit yes.
    existing=$(envval DB_DSN | sed 's#^[^/]*//[^/]*/\([^?]*\).*#\1#')
    for db in "$RENTALS_DB" "$PLATFORM_DB"; do
        if { [ "$db" = "$existing" ] || [ "$db" = willcoll ]; } && [ "$(envval CONFIRM_EXISTING_DB)" != yes ]; then
            dev_die "$db is your existing database. The dev stack would migrate and seed it (000045 drops its identity tables). Pick other names (the defaults are fine) or set CONFIRM_EXISTING_DB=yes to proceed."
        fi
    done

    APP_PW=$(envval DB_APP_PASSWORD)
    [ -n "$APP_PW" ] || APP_PW=$(dsn_password "$(envval DB_DSN)")
    ADMIN_PW=$(envval DB_ADMIN_PASSWORD)
    PAPP_PW=$(envval PLATFORM_DB_APP_PASSWORD)
    PADM_PW=$(envval PLATFORM_DB_ADMIN_PASSWORD)
    [ -n "$APP_PW" ] || dev_die "no willcoll_app password: set DB_DSN (or DB_APP_PASSWORD) in backend/.env"
    [ -n "$ADMIN_PW" ] || dev_die "DB_ADMIN_PASSWORD is not set in backend/.env"

    # The platform roles: an existing role keeps its password. Try the .env
    # one, then the throwaway one the integration-test scripts create.
    if role_exists willcoll_platform_app && [ -z "$PAPP_PW" ]; then
        can_login willcoll_platform_app willcoll-test-willcoll_platform_app && PAPP_PW=willcoll-test-willcoll_platform_app
    fi
    if role_exists willcoll_platform_admin && [ -z "$PADM_PW" ]; then
        can_login willcoll_platform_admin willcoll-test-willcoll_platform_admin && PADM_PW=willcoll-test-willcoll_platform_admin
    fi
    PAPP_PW=${PAPP_PW:-willcoll-test-willcoll_platform_app}
    PADM_PW=${PADM_PW:-willcoll-test-willcoll_platform_admin}

    # Owner (migration) DSNs for the two dev databases. Read by dev_setup.sh,
    # which sources this file; deliberately NOT exported, since they carry the
    # owner password and would reach every child process.
    # shellcheck disable=SC2034
    OWNER_PLATFORM=$(swap_db "$MIGRATE_DSN" "$PLATFORM_DB")
    # shellcheck disable=SC2034
    OWNER_RENTALS=$(swap_db "$MIGRATE_DSN" "$RENTALS_DB")
    RENTALS_APP=$(role_dsn "$RENTALS_DB" willcoll_app "$APP_PW")
    RENTALS_ADMIN=$(role_dsn "$RENTALS_DB" willcoll_admin "$ADMIN_PW")
    PLATFORM_APP=$(role_dsn "$PLATFORM_DB" willcoll_platform_app "$PAPP_PW")
    PLATFORM_ADMIN=$(role_dsn "$PLATFORM_DB" willcoll_platform_admin "$PADM_PW")

    PLATFORM_PORT=$(envval PLATFORM_PORT)
    PLATFORM_PORT=${PLATFORM_PORT:-4100}
    RENTALS_PORT=$(envval RENTALS_PORT)
    RENTALS_PORT=${RENTALS_PORT:-$(envval PORT)}
    RENTALS_PORT=${RENTALS_PORT:-4000}
    [ "$RENTALS_PORT" != "$PLATFORM_PORT" ] || dev_die "RENTALS_PORT and PLATFORM_PORT are both $RENTALS_PORT"
    FAKE_SMS=$(envval FAKE_SMS_ADDR)
    FAKE_SMS=${FAKE_SMS:-127.0.0.1:14901}
    FAKE_PAYHERO=$(envval FAKE_PAYHERO_ADDR)
    FAKE_PAYHERO=${FAKE_PAYHERO:-127.0.0.1:14902}
    DEV_FAKES=$(envval DEV_FAKES)
    DEV_FAKES=${DEV_FAKES:-true}
}

dsn_user() { echo "$1" | sed 's#^[a-z]*://\([^:@]*\).*#\1#'; }

# default_env NAME VALUE: export VALUE unless NAME already has one (env or .env).
default_env() {
    cur=$(envval "$1")
    if [ -n "$cur" ]; then export "$1=$cur"; else export "$1=$2"; fi
}

# dev_service_env <platform|rentals>: export what that API needs, in the
# current shell (call it in a subshell). Settings you already have in
# backend/.env win, except the database and port wiring, which the dev stack
# owns, and (with DEV_FAKES) the SMS / PayHero / SMTP endpoints, which point
# at local stand-ins so nothing real is called or sent.
dev_service_env() {
    export ENV=development
    default_env LIMITER_ENABLED false
    default_env CORS_TRUSTED_ORIGINS "http://localhost:3000 http://localhost:3001"
    default_env PAYHERO_WEBHOOK_SECRET dev-webhook-secret
    if [ "$DEV_FAKES" != false ]; then
        export SMTP_HOST=
        export PAYHERO_BASE_URL="http://$FAKE_PAYHERO" PAYHERO_API_KEY=dev
        export PAYHERO_BILLING_CHANNEL_ID=BILLING-DEV PAYHERO_COLLECTIONS_CHANNEL_ID=COLLECTIONS-DEV PAYHERO_ACCOUNT_ID=1
        export AFRICASTALKING_API_KEY=dev AFRICASTALKING_USERNAME=dev "AFRICASTALKING_BASE_URL=http://$FAKE_SMS"
    fi
    case "$1" in
    platform)
        # frontend/platform (:3001) owns the sign-in pages, so the links in
        # activation / password-reset / staff-invite emails point there.
        # (FRONTEND_URL in .env is rentals-api's; platform has its own override.)
        FRONTEND_URL=$(envval PLATFORM_FRONTEND_URL)
        export FRONTEND_URL="${FRONTEND_URL:-http://localhost:3001}"
        export PORT="$PLATFORM_PORT" DB_DSN="$PLATFORM_APP" DB_ADMIN_DSN="$PLATFORM_ADMIN"
        export PAYHERO_CALLBACK_BASE_URL="http://127.0.0.1:$PLATFORM_PORT"
        export RENTALS_API_URL="http://127.0.0.1:$RENTALS_PORT" # the system health page asks rentals-api for its own report
        ADMIN_NAME=$(envval ADMIN_NAME) ADMIN_EMAIL=$(envval ADMIN_EMAIL) ADMIN_PASSWORD=$(envval ADMIN_PASSWORD)
        export ADMIN_NAME ADMIN_EMAIL ADMIN_PASSWORD
        ;;
    rentals)
        default_env FRONTEND_URL http://localhost:3000
        export PORT="$RENTALS_PORT" DB_DSN="$RENTALS_APP" DB_ADMIN_DSN="$RENTALS_ADMIN"
        export PLATFORM_DB_DSN="$PLATFORM_APP" PLATFORM_DB_ADMIN_DSN="$PLATFORM_ADMIN"
        export PAYHERO_CALLBACK_BASE_URL="http://127.0.0.1:$RENTALS_PORT"
        default_env PAY_SESSION_SECRET dev-pay-session-secret-0123456789abcdef
        default_env QR_BASE_URL http://localhost:3000
        ;;
    esac
}
