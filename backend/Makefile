# A stray .env silently overrides DSNs and passwords. NO_DOTENV=1 ignores it:
#   make NO_DOTENV=1 test/integration
ifndef NO_DOTENV
-include .env
export
endif

.PHONY: help staging/smoke dev/setup dev/up run/platform-api run/rentals-api build/rentals-api run/api build/api test audit test/race test/integration tidy db/psql db/roles db/migrations/new db/migrations/up db/migrations/down sqlc/generate dev/test-plan dev/expire-trial dev/reset-trial dev/set-plan db/backfill-platform rehearse/cutover dev/payhero staging/up staging/down staging/logs staging/ps staging/seed staging/check-rls staging/export-ca staging/reset

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
	@echo "  test/race              go test -race ./... inside golang:<go.mod version> via Docker, no backend/.env visible (the way to get -race on Windows)"
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
	@echo "  staging/up             local staging in Docker (deploy/local): build, migrate, start everything, wait until healthy"
	@echo "  staging/down           stop it (data volumes kept)"
	@echo "  staging/logs           follow logs; SVC=<service> for one (e.g. SVC=rentals-api)"
	@echo "  staging/ps             service status"
	@echo "  staging/seed           demo firm + TEST plan + Super Admin (RESEED=yes replaces an existing demo firm)"
	@echo "  staging/smoke          check the running stack accepts what deploy/local/.env says (logins, signed webhook, health)"
	@echo "  staging/check-rls     prove row-level security applies to the API roles"
	@echo "  staging/export-ca      write Caddy's root CA to deploy/local/caddy-root.crt (to trust it: docs/dev-setup.md)"
	@echo "  staging/reset          delete the database AND Caddy CA volumes; needs CONFIRM=yes"
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

# `go test -race` in a clean Linux container: the race detector needs cgo,
# which a bare Windows machine lacks. The source is streamed in with tar, so
# nothing is bind-mounted and backend/.env (and bin/) never reach the
# container, and no host environment variable is passed. Module and build
# caches live in named volumes. The Go version is read from go.mod.
# MSYS_NO_PATHCONV stops Git Bash rewriting /src and the volume paths.
go_version = $(shell awk '/^go /{print $$2}' go.mod)

test/race:
	tar --exclude=./.env --exclude=./bin -cf - . | MSYS_NO_PATHCONV=1 docker run --rm -i \
		-v willcoll-gomod:/go/pkg/mod -v willcoll-gobuild:/root/.cache/go-build \
		-e GOFLAGS=-buildvcs=false -w /src golang:$(go_version) \
		sh -c 'tar -xf - && go test -race -vet=off ./...'

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

# ---- Local staging: the whole stack in Docker (deploy/local, docs/dev-setup.md) ----
# Reads deploy/local/.env only (copied from .env.example on first `up`); the
# containers never see backend/.env. MSYS_NO_PATHCONV keeps Git Bash from
# rewriting container paths.
STAGING_DIR = ../deploy/local
# The `-include .env` + `export` at the top of this file puts backend/.env in
# every recipe's environment, and docker compose lets the process environment
# beat --env-file: staging silently ran with backend/.env's ADMIN_EMAIL, webhook
# secret and so on (C4.0). So every variable named in backend/.env or in the
# staging env files is unset for the compose call; only deploy/local/.env is read.
STAGING_UNSET = $(addprefix -u ,$(sort $(shell sed -n 's/^[[:space:]]*\([A-Za-z_][A-Za-z0-9_]*\)=.*/\1/p' .env $(STAGING_DIR)/.env.example $(STAGING_DIR)/.env 2>/dev/null)))
STAGING_COMPOSE = env $(STAGING_UNSET) MSYS_NO_PATHCONV=1 docker compose -f $(STAGING_DIR)/docker-compose.yml --env-file $(STAGING_DIR)/.env

staging/up:
	@test -f $(STAGING_DIR)/.env || { cp $(STAGING_DIR)/.env.example $(STAGING_DIR)/.env && echo "created $(STAGING_DIR)/.env from .env.example"; }
	$(STAGING_COMPOSE) up -d --build --wait
	@echo ""
	@echo "up. Next: make staging/seed (first time), trust Caddy's CA once (docs/dev-setup.md), then open the platform URL from $(STAGING_DIR)/.env"

staging/down:
	$(STAGING_COMPOSE) down

staging/logs:
	$(STAGING_COMPOSE) logs -f --tail=100 $(SVC)

staging/ps:
	$(STAGING_COMPOSE) ps -a

# The seeder fails if the demo firm already exists: RESEED=yes replaces it.
# --build because `run` never rebuilds an existing image and `up --build` skips
# the profile-only tool images: a stale seed image once failed against the
# current RLS scope rules (C4.0).
staging/seed:
	$(STAGING_COMPOSE) run --rm --build --quiet-build seed $(if $(filter yes,$(RESEED)),-reset)
	$(STAGING_COMPOSE) run --rm --build --quiet-build devtools test-plan
	$(STAGING_COMPOSE) run --rm --build --quiet-build devtools claim-channels

# Is the stack accepting exactly what deploy/local/.env says? (C4.0) Super Admin
# and firm-owner logins, a signed webhook, every health endpoint, over HTTPS.
staging/smoke:
	sh ./scripts/staging_smoke.sh

staging/check-rls:
	$(STAGING_COMPOSE) exec -T postgres psql -U postgres -v ON_ERROR_STOP=1 -f /opt/willcoll/check_rls.sql

# Caddy's local root certificate, to import into the OS/browser trust store once.
staging/export-ca:
	$(STAGING_COMPOSE) cp caddy:/data/caddy/pki/authorities/local/root.crt $(STAGING_DIR)/caddy-root.crt

staging/reset:
	@test "$(CONFIRM)" = yes || { echo "staging/reset deletes the staging databases and Caddy's local CA. Re-run with CONFIRM=yes"; exit 1; }
	$(STAGING_COMPOSE) --profile tools down -v --remove-orphans

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
