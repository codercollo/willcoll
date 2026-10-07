#!/usr/bin/env sh
# Shared by scripts/test_integration.sh and scripts/rehearse_cutover.sh: finds
# the dev cluster's settings, makes sure the platform roles exist, and derives
# DSNs for throwaway databases. SOURCE it (. scripts/lib_testdb.sh) from the
# backend directory; it defines functions and variables only and runs nothing
# destructive. Nothing here ever prints a DSN.
#
# Reads, from the environment or backend/.env (environment wins):
#   MIGRATE_DSN        owner/superuser DSN of the dev database (database name
#                      must be "willcoll"; scratch databases are derived from it)
#   DB_DSN             the API DSN (role willcoll_app)
#   DB_ADMIN_PASSWORD  password of the willcoll_admin role
#   PLATFORM_DB_APP_PASSWORD, PLATFORM_DB_ADMIN_PASSWORD
#       Passwords of willcoll_platform_app / willcoll_platform_admin (roles are
#       cluster-wide). REQUIRED when those roles already exist - an existing
#       role's password is never rewritten. When they do not exist yet (a
#       machine that never set up willcoll_platform) they are created with
#       these passwords, or fixed throwaway test ones if unset.

# Read only what is needed from .env, without sourcing it: the file is also
# consumed by make, and a value like SMTP_SENDER=Willcoll <a@b> is not valid
# shell.
envval() {
    eval "current=\${$1:-}"
    if [ -n "$current" ]; then
        printf '%s' "$current"
    elif [ -f .env ]; then
        sed -n "s/^$1=//p" .env | head -n 1 | sed 's/^"\(.*\)"$/\1/'
    fi
}

role_exists() {
    [ "$(psql "$MIGRATE_DSN" -Atc "SELECT 1 FROM pg_roles WHERE rolname = '$1'")" = "1" ]
}

# ensure_platform_role <name> <password-from-env> <BYPASSRLS|NOBYPASSRLS>
# Prints a password only when it created the role with a fallback one.
ensure_platform_role() {
    name=$1 password=$2 bypass=$3
    fallback="willcoll-test-$name"
    if role_exists "$name"; then
        [ -n "$password" ] && return 0
        # No password configured: fine if an earlier run of these scripts
        # created the role with the throwaway one - check that it still logs in.
        probe=$(echo "$DB_DSN" | sed "s#/willcoll?#/postgres?#; s#//willcoll_app:[^@]*@#//$name:$fallback@#")
        if psql "$probe" -Atc "SELECT 1" >/dev/null 2>&1; then
            echo "$fallback"
            return 0
        fi
        echo "role $name already exists with a password this script does not know; set PLATFORM_DB_APP_PASSWORD and PLATFORM_DB_ADMIN_PASSWORD (backend/.env) so its DSN can be built" >&2
        return 1
    fi
    if [ -z "$password" ]; then
        echo "creating role $name with a throwaway test password (set PLATFORM_DB_*_PASSWORD to choose one)" >&2
        password=$fallback
    fi
    psql "$MIGRATE_DSN" -qc "CREATE ROLE $name LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE $bypass PASSWORD '$password'" >/dev/null
    echo "$password"
}

# testdb_init loads the settings and makes sure the platform roles exist.
testdb_init() {
    MIGRATE_DSN=$(envval MIGRATE_DSN)
    DB_DSN=$(envval DB_DSN)
    DB_ADMIN_PASSWORD=$(envval DB_ADMIN_PASSWORD)
    PLATFORM_DB_APP_PASSWORD=$(envval PLATFORM_DB_APP_PASSWORD)
    PLATFORM_DB_ADMIN_PASSWORD=$(envval PLATFORM_DB_ADMIN_PASSWORD)

    : "${MIGRATE_DSN:?MIGRATE_DSN is not set}"
    : "${DB_DSN:?DB_DSN is not set}"
    : "${DB_ADMIN_PASSWORD:?DB_ADMIN_PASSWORD is not set}"

    case "$MIGRATE_DSN$DB_DSN" in
    */willcoll\?*/willcoll\?*) ;;
    *)
        echo "expected MIGRATE_DSN and DB_DSN to name a database called willcoll" >&2
        return 1
        ;;
    esac

    out=$(ensure_platform_role willcoll_platform_app "$PLATFORM_DB_APP_PASSWORD" NOBYPASSRLS) || return 1
    [ -n "$out" ] && PLATFORM_DB_APP_PASSWORD=$out
    out=$(ensure_platform_role willcoll_platform_admin "$PLATFORM_DB_ADMIN_PASSWORD" BYPASSRLS) || return 1
    [ -n "$out" ] && PLATFORM_DB_ADMIN_PASSWORD=$out
    return 0
}

# DSNs for a database <db> on the dev cluster, by role.
owner_dsn() { echo "$MIGRATE_DSN" | sed "s#/willcoll?#/$1?#"; }
app_dsn() { echo "$DB_DSN" | sed "s#/willcoll?#/$1?#"; }
admin_dsn() { app_dsn "$1" | sed "s#//willcoll_app:[^@]*@#//willcoll_admin:${DB_ADMIN_PASSWORD}@#"; }
platform_app_dsn() { app_dsn "$1" | sed "s#//willcoll_app:[^@]*@#//willcoll_platform_app:${PLATFORM_DB_APP_PASSWORD}@#"; }
platform_admin_dsn() { app_dsn "$1" | sed "s#//willcoll_app:[^@]*@#//willcoll_platform_admin:${PLATFORM_DB_ADMIN_PASSWORD}@#"; }

# migrate_up <dir> <owner DSN> [target version]: golang-migrate when installed,
# else psql applies the .up.sql files in order (stopping after the target).
migrate_up() {
    dir=$1 dsn=$2 target=${3:-}
    if command -v migrate >/dev/null 2>&1; then
        if [ -n "$target" ]; then
            migrate -path "$dir" -database "$dsn" goto "$target"
        else
            migrate -path "$dir" -database "$dsn" up
        fi
    else
        psql "$dsn" -q -c "CREATE TABLE IF NOT EXISTS schema_migrations (version bigint PRIMARY KEY, dirty boolean NOT NULL)"
        for f in "$dir"/*.up.sql; do
            if [ -n "$target" ]; then
                v=$(basename "$f" | sed 's/^0*\([0-9]*\)_.*/\1/')
                [ "$v" -gt "$target" ] && break
            fi
            psql "$dsn" -q -v ON_ERROR_STOP=1 -f "$f" >/dev/null
        done
    fi
}
