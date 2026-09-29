#!/usr/bin/env sh
# Regenerates the repository layer: sqlc queries from internal/db/query/*.sql
# (schema read from migrations/), then gomock mocks of the sqlc Querier and
# the db.Store interfaces. Run via `make sqlc/generate` whenever a query or
# migration changes, and commit the output.
set -eu

cd "$(dirname "$0")/.."

MOCKGEN="go run go.uber.org/mock/mockgen@v0.6.0"

sqlc vet
sqlc generate

$MOCKGEN -destination=internal/db/mock/querier.go -package=mock \
    github.com/codercollo/willcoll/backend/internal/db/sqlc Querier
$MOCKGEN -destination=internal/db/mock/store.go -package=mock \
    github.com/codercollo/willcoll/backend/internal/db Store
