# Willcoll backend images (local staging and production only: local development
# is plain Go and Postgres, see CLAUDE.md). One Dockerfile; build from backend/:
#
#   # an API or tool: SERVICE = platform-api | rentals-api | seed | devtools
#   docker build -f build/Dockerfile --build-arg SERVICE=rentals-api \
#     --build-arg VERSION=$(git describe --always --tags --long) \
#     --build-arg BUILD_TIME=$(date -u +%Y-%m-%dT%H:%M:%SZ) -t willcoll-rentals-api .
#
#   # the one-shot migration runner (never run from an API at boot)
#   docker build -f build/Dockerfile --target migrate -t willcoll-migrate .
#   docker run --rm willcoll-migrate -path /migrations/platform \
#     -database "$PLATFORM_MIGRATE_DSN" up
#
# Dependencies are not vendored (docs/adr/0005-no-vendoring.md): the module
# download is its own layer, cached until go.mod or go.sum change. The build
# context's .dockerignore keeps .env files, bin/ and .git out; nothing is
# bind-mounted at run time.

# ---- builder: the service binary -------------------------------------------
FROM golang:1.26-alpine AS builder

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download && go mod verify

COPY . .

ARG SERVICE=rentals-api
ARG VERSION=dev
ARG BUILD_TIME=unknown

# A static binary. -X sets the release version and build time that
# internal/shared/vcs reports (GET /v1/healthcheck); .git is not in the build
# context, so the version comes from the build args, not from -buildvcs.
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -buildvcs=false \
      -ldflags="-s -w -X main.releaseVersion=${VERSION} -X main.buildTime=${BUILD_TIME}" \
      -o /bin/service ./cmd/${SERVICE}

# ---- migrate-builder: golang-migrate (same version as CI) --------------------
FROM golang:1.26-alpine AS migrate-builder
RUN CGO_ENABLED=0 GOOS=linux go install -trimpath -tags 'postgres' \
      -ldflags="-s -w" \
      github.com/golang-migrate/migrate/v4/cmd/migrate@v4.19.1

# ---- base: minimal non-root runtime ------------------------------------------
# Alpine rather than distroless: the compose healthchecks need a client (busybox
# wget) and the tools (seed, devtools) are easier to run with a shell. CA
# certificates are included (PayHero and Africa's Talking are HTTPS) and tzdata
# (pkg/moneyfmt loads Africa/Nairobi at init and panics without it).
FROM alpine:3.22 AS base
RUN apk add --no-cache ca-certificates tzdata \
 && addgroup -S -g 10001 willcoll \
 && adduser -S -u 10001 -G willcoll -H -s /sbin/nologin willcoll

# ---- migrate: one-shot migration runner (target: migrate) --------------------
FROM base AS migrate
COPY --from=migrate-builder /go/bin/migrate /usr/local/bin/migrate
COPY migrations/ /migrations/
USER willcoll:willcoll
ENTRYPOINT ["migrate"]

# ---- runtime: the default target ---------------------------------------------
# Last stage, so a plain `docker build` produces this one.
FROM base AS runtime
COPY --from=builder /bin/service /service
EXPOSE 4000 4100
USER willcoll:willcoll
ENTRYPOINT ["/service"]
