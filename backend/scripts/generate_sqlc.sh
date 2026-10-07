#!/usr/bin/env sh
# Regenerates the repository layer for every sqlc entry in sqlc.yaml
# (rentals: internal/rentals/db/query/*.sql, schema migrations/; platform:
# internal/platform/db/query/*.sql, schema migrations/platform/), then
# gomock mocks of each package's Querier and Store interfaces. Run via
# `make sqlc/generate` whenever a query or migration changes, and commit
# the output.
set -eu

cd "$(dirname "$0")/.."

MOCKGEN="go run go.uber.org/mock/mockgen@v0.6.0"

sqlc vet
sqlc generate

$MOCKGEN -destination=internal/rentals/db/mock/querier.go -package=mock \
    github.com/codercollo/willcoll/backend/internal/rentals/db/sqlc Querier
$MOCKGEN -destination=internal/rentals/db/mock/store.go -package=mock \
    github.com/codercollo/willcoll/backend/internal/rentals/db Store

$MOCKGEN -destination=internal/platform/db/mock/querier.go -package=mock \
    github.com/codercollo/willcoll/backend/internal/platform/db/sqlc Querier
$MOCKGEN -destination=internal/platform/db/mock/store.go -package=mock \
    github.com/codercollo/willcoll/backend/internal/platform/db Store
