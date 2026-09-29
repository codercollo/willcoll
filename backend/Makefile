-include .env
export

.PHONY: help run/api build/api test audit test/integration tidy db/psql db/roles db/migrations/new db/migrations/up db/migrations/down sqlc/generate dev/test-plan dev/expire-trial dev/reset-trial dev/set-plan

help:
	@echo "usage: make [target]"
	@echo ""
	@echo "  run/api                run the cmd/api application"
	@echo "  build/api              build a static binary with the version embedded"
	@echo "  test                   run the tests with the race detector (RACE= to turn it off)"
	@echo "  audit                  format, vet, staticcheck, golangci-lint and test all code"
	@echo "  test/integration       run every test, incl. Postgres integration + concurrency, on a throwaway database"
	@echo "  tidy                   format code and tidy go.mod"
	@echo "  db/psql                connect to the DB using psql (as the API role)"
	@echo "  db/roles               create/update the willcoll_app and willcoll_admin roles"
	@echo "  db/migrations/new      create a new migration pair, e.g. make db/migrations/new name=create_x"
	@echo "  db/migrations/up       apply all up migrations"
	@echo "  db/migrations/down     roll back all migrations"
	@echo "  sqlc/generate          regenerate sqlc queries and gomock mocks"
	@echo "  dev/test-plan          upsert the TEST - KES 10 subscription plan (ENV=development only)"
	@echo "  dev/expire-trial       EMAIL=<email>: end that manager's trial/subscription yesterday (ENV=development only)"
	@echo "  dev/reset-trial        EMAIL=<email>: restore a fresh trial and drop any subscription (ENV=development only)"
	@echo "  dev/set-plan           EMAIL=<email> PLAN=<plan name>: put a manager directly on a plan, no payment (ENV=development only)"

run/api:
	go run ./cmd/api

# Version embedding: the release version comes from git (tag, else commit),
# the time from the clock; internal/vcs adds the VCS revision at run time.
# No shell operators inside $(shell ...): some Windows makes have no sh.
git_description = $(or $(shell git describe --always --dirty --tags --long),dev)
current_time = $(or $(shell date -u +%Y-%m-%dT%H:%M:%SZ),unknown)
linker_flags = -s -w -X main.releaseVersion=${git_description} -X main.buildTime=${current_time}

# A static binary: no cgo, so it runs in a minimal container image.
build/api:
	CGO_ENABLED=0 go build -trimpath -ldflags='${linker_flags}' -o=./bin/api ./cmd/api

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
	$(MAKE) test

# Builds willcoll_verify beside the dev database, migrates it, runs the whole
# suite against it and drops it. Never touches the dev database.
test/integration:
	sh ./scripts/test_integration.sh

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
	migrate create -seq -ext=.sql -dir=./migrations ${name}

db/migrations/up:
	migrate -path=./migrations -database=${MIGRATE_DSN} up

db/migrations/down:
	migrate -path=./migrations -database=${MIGRATE_DSN} down

sqlc/generate:
	sh ./scripts/generate_sqlc.sh

# Fill the dev database with a realistic demo firm (see cmd/seed). ARGS=-reset replaces it.
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
