#!/usr/bin/env sh
# Runs ONE API with the dev stack's wiring (databases, ports, local fakes):
#   sh scripts/dev_run.sh platform      # make run/platform-api
#   sh scripts/dev_run.sh rentals       # make run/rentals-api
# Plain `go run`, no hot reload. Run `make dev/setup` first. Use `make dev/up`
# to run both together with the SMS/PayHero stand-ins.
set -eu
cd "$(dirname "$0")/.."
. ./scripts/lib_dev.sh

svc=${1:-}
case "$svc" in
platform | rentals) ;;
*) dev_die "usage: sh scripts/dev_run.sh platform|rentals" ;;
esac

dev_init
dev_service_env "$svc"
exec go run "./cmd/$svc-api"
