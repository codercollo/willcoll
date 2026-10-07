-include .env
export

.PHONY: help dev/setup dev/up run/platform-api run/rentals-api build/rentals-api run/api build/api test audit test/integration tidy db/psql db/roles db/migrations/new db/migrations/up db/migrations/down sqlc/generate dev/test-plan dev/expire-trial dev/reset-trial dev/set-plan db/backfill-platform rehearse/cutover dev/payhero

help:
	@echo "usage: make [target]"
	@echo ""
	@echo "  dev/setup              one-time, re-runnable: dev databases + roles + migrations + demo firm (scripts/dev_setup.sh)"
	@echo "  dev/up                 run platform-api + rentals-api (+ local SMS/PayHero stand-ins) together; Ctrl+C stops all"
	@echo "  run/platform-api       run cmd/platform-api against the dev databases (plain go run)"
	@echo "  run/rentals-api        run cmd/rentals-api against the dev databases (plain go run)"
	@echo "  build/rentals-api      build a static binary with the version embedded"
	@echo "  run/api, build/api     deprecated aliases for the two targets above"
	@echo "  test                   run the tests with the race detector (RACE= to turn it off)"
	@echo "  audit                  format, vet, staticcheck, golangci-lint and test all code"
	@echo "  test/integration       run every test, incl. Postgres integration + concurrency, on throwaway rentals + platform databases (INTEGRATION=1: a missing DSN fails, not skips)"
	@echo "  rehearse/cutover       rehearse the whole platform-split cutover end to end on throwaway databases (ARGS=--with-gate to run the gate first)"
	@echo "  tidy                   format code and tidy go.mod"
	@echo "  db/psql                connect to the DB using psql (as the API role)"
	@echo "  db/roles               create/update the willcoll_app and willcoll_admin roles"
	@echo "  db/migrations/new      create a new migration pair, e.g. make db/migrations/new name=create_x"
	@echo "  db/migrations/up       apply all up migrations"
	@echo "  db/migrations/down     roll back all migrations"
	@echo "  sqlc/generate          regenerate sqlc queries and gomock mocks"
	@echo "  dev/payhero            result=<success|cancelled|insufficient|failed> [id=<ref>]: answer a pending fake STK push by hand"
	@echo "  dev/test-plan          upsert the TEST - KES 10 subscription plan (ENV=development only)"
	@echo "  dev/expire-trial       EMAIL=<email>: end that manager's trial/subscription yesterday (ENV=development only)"
	@echo "  dev/reset-trial        EMAIL=<email>: restore a fresh trial and drop any subscription (ENV=development only)"
	@echo "  dev/set-plan           EMAIL=<email> PLAN=<plan name>: put a manager directly on a plan, no payment (ENV=development only)"
	@echo "  db/backfill-platform   SOURCE_DSN=<old willcoll admin DSN> PLATFORM_DSN=<willcoll_platform admin DSN>: copy platform-owned rows + seed module_usage (build-phases.txt 3d; runs in any ENV)"

# Local dev stack (docs/dev-setup.md). These derive their database DSNs and
# ports for the DEV_* databases (scripts/lib_dev.sh) instead of using DB_DSN
# as written in .env, so your existing database is never touched. Run
# dev/setup once first.
dev/setup:
	sh ./scripts/dev_setup.sh

dev/up:
	sh ./scripts/dev_up.sh

# Completes (or fails) the latest unanswered fake STK push by hand:
#   make dev/payhero result=cancelled [id=<external reference>]
# result: success | cancelled | insufficient | failed (docs/dev-setup.md).
dev/payhero:
	@test -n "$(result)" || { echo "usage: make dev/payhero result=<success|cancelled|insufficient|failed> [id=<external reference>]"; exit 1; }
	go run ./cmd/rehearse payhero result=$(result) $(if $(id),id=$(id))

run/platform-api:
	sh ./scripts/dev_run.sh platform

run/rentals-api:
	sh ./scripts/dev_run.sh rentals

# Deprecated: use run/rentals-api. Kept until every script/doc referencing
# run/api is updated (project-structure.txt Part D step 2).
run/api: run/rentals-api

# Version embedding: the release version comes from git (tag, else commit),
# the time from the clock; internal/shared/vcs adds the VCS revision at run time.
# No shell operators inside $(shell ...): some Windows makes have no sh.
git_description = $(or $(shell git describe --always --dirty --tags --long),dev)
current_time = $(or $(shell date -u +%Y-%m-%dT%H:%M:%SZ),unknown)
linker_flags = -s -w -X main.releaseVersion=${git_description} -X main.buildTime=${current_time}

# A static binary: no cgo, so it runs in a minimal container image.
build/rentals-api:
	CGO_ENABLED=0 go build -trimpath -ldflags='${linker_flags}' -o=./bin/rentals-api ./cmd/rentals-api

# Deprecated: use build/rentals-api. Kept until every script/doc referencing
# build/api is updated (project-structure.txt Part D step 2).
build/api: build/rentals-api

# The race detector needs cgo (a C compiler). Where there is none, for
# example a bare Windows setup, run `make test RACE=`.
RACE ?= -race

test:
	go test ${RACE} -vet=off ./...

# Everything CI runs before a merge. staticcheck and golangci-lint are the
# only tools not installed by `go`; the first two lines say how to get them.
audit: tidy
	@command -v staticcheck >/dev/null || { echo "staticcheck missing: go install honnef.co/go/tools/cmd/staticcheck@latest"; exit 1; }
	@command -v golangci-lint >/dev/null || { echo "golangci-lint missing: see https://golangci-lint.run/welcome/install/"; exit 1; }
	go vet ./...
	staticcheck ./...
	golangci-lint run ./...
	"$(MAKE)" test

# Builds willcoll_verify beside the dev database, migrates it, runs the whole
# suite against it and drops it. Never touches the dev database.
test/integration:
	INTEGRATION=1 sh ./scripts/test_integration.sh

# The platform-split cutover, rehearsed from nothing on throwaway databases
# with both real APIs and an HTTP smoke test (scripts/rehearse_cutover.sh).
# ARGS=--with-gate also runs the full gate first.
rehearse/cutover:
	sh ./scripts/rehearse_cutover.sh $(ARGS)

tidy:
	go fmt ./...
	go mod tidy -v

db/psql:
	psql ${DB_DSN}

# The API connects as willcoll_app (DB_DSN), which is subject to RLS.
# Roles and migrations run as the owning/superuser role (MIGRATE_DSN):
# superusers bypass RLS, so that DSN must never be the API's.
db/roles:
	@psql ${MIGRATE_DSN} -v app_password=${DB_APP_PASSWORD} -v admin_password=${DB_ADMIN_PASSWORD} -f ./scripts/db_roles.sql

db/migrations/new:
	migrate create -seq -ext=.sql -dir=./migrations/rentals ${name}

db/migrations/up:
	migrate -path=./migrations/rentals -database=${MIGRATE_DSN} up

db/migrations/down:
	migrate -path=./migrations/rentals -database=${MIGRATE_DSN} down

sqlc/generate:
	sh ./scripts/generate_sqlc.sh

# Fill the dev databases with a realistic demo firm (see cmd/seed): identity,
# plans and subscription into willcoll_platform (PLATFORM_DSN), the portfolio
# and ledgers into willcoll. ARGS=-reset replaces it (needs MIGRATE_DSN and
# PLATFORM_MIGRATE_DSN, owner roles).
db/seed:
	go run ./cmd/seed $(ARGS)

# Dev-only helpers for testing the trial/subscription paywall (cmd/devtools).
# All three refuse to run unless ENV is unset or "development".
dev/test-plan:
	go run ./cmd/devtools test-plan

dev/expire-trial:
	@test -n "$(EMAIL)" || { echo "usage: make dev/expire-trial EMAIL=<manager email>"; exit 1; }
	go run ./cmd/devtools expire-trial -email=$(EMAIL)

dev/reset-trial:
	@test -n "$(EMAIL)" || { echo "usage: make dev/reset-trial EMAIL=<manager email>"; exit 1; }
	go run ./cmd/devtools reset-trial -email=$(EMAIL)

dev/set-plan:
	@test -n "$(EMAIL)" || { echo "usage: make dev/set-plan EMAIL=<manager email> PLAN=<plan name>"; exit 1; }
	@test -n "$(PLAN)" || { echo "usage: make dev/set-plan EMAIL=<manager email> PLAN=<plan name>"; exit 1; }
	go run ./cmd/devtools set-plan -email=$(EMAIL) -plan="$(PLAN)"

# Copies platform-owned rows (admins, managers, subscription_plans,
# subscriptions, subscription_invoices, system_metadata) from the old
# willcoll database into willcoll_platform, and seeds module_usage for
# every manager (build-phases.txt Phase 3 step 3d). Idempotent; safe to
# re-run. Unlike the dev/* targets above, this runs in ANY environment —
# it exists for the real staging/production cutover.
db/backfill-platform:
	@test -n "$(SOURCE_DSN)" || { echo "usage: make db/backfill-platform SOURCE_DSN=<old willcoll admin DSN> PLATFORM_DSN=<willcoll_platform admin DSN>"; exit 1; }
	@test -n "$(PLATFORM_DSN)" || { echo "usage: make db/backfill-platform SOURCE_DSN=<old willcoll admin DSN> PLATFORM_DSN=<willcoll_platform admin DSN>"; exit 1; }
	SOURCE_DSN=$(SOURCE_DSN) PLATFORM_DSN=$(PLATFORM_DSN) go run ./cmd/devtools backfill-platform
