#!/bin/sh
# Runs once, on first boot of an empty data directory (the official postgres
# image executes /docker-entrypoint-initdb.d/*.sh as the superuser). It only
# turns environment variables into psql variables for init.sql, so the same
# init.sql works in every environment. A missing password stops the boot.
set -eu

need() {
	eval "v=\${$1:-}"
	[ -n "$v" ] || {
		echo "init.sh: $1 is not set" >&2
		exit 1
	}
}

for n in RENTALS PLATFORM ESTATES WASTE; do need "${n}_OWNER_PASSWORD"; need "${n}_APP_PASSWORD"; done
need RENTALS_ADMIN_PASSWORD
need PLATFORM_ADMIN_PASSWORD

exec psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname postgres \
	-v rentals_owner_pw="$RENTALS_OWNER_PASSWORD" -v rentals_app_pw="$RENTALS_APP_PASSWORD" -v rentals_admin_pw="$RENTALS_ADMIN_PASSWORD" \
	-v platform_owner_pw="$PLATFORM_OWNER_PASSWORD" -v platform_app_pw="$PLATFORM_APP_PASSWORD" -v platform_admin_pw="$PLATFORM_ADMIN_PASSWORD" \
	-v estates_owner_pw="$ESTATES_OWNER_PASSWORD" -v estates_app_pw="$ESTATES_APP_PASSWORD" \
	-v waste_owner_pw="$WASTE_OWNER_PASSWORD" -v waste_app_pw="$WASTE_APP_PASSWORD" \
	-f /opt/willcoll/init.sql
