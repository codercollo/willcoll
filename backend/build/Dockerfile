# Willcoll API image (Staging / Production only: local development is plain
# Go and Postgres, see CLAUDE.md). Build from backend/:
#
#   docker build -f build/Dockerfile \
#     --build-arg VERSION=$(git describe --always --tags --long) \
#     --build-arg BUILD_TIME=$(date -u +%Y-%m-%dT%H:%M:%SZ) -t willcoll-api .
#
# Dependencies are not vendored (see docs/adr/0005-no-vendoring.md): the module
# download is its own layer, so it is cached until go.mod or go.sum change.

# ---- Stage 1: builder ------------------------------------------------------
FROM golang:1.26-alpine AS builder

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download && go mod verify

COPY . .

ARG VERSION=dev
ARG BUILD_TIME=unknown

# A static binary, so it runs in a minimal image. -X sets the release version
# and build time reported by GET /v1/healthcheck; the VCS revision is read at
# run time from the binary (-buildvcs) when the .git directory is in the
# build context.
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath \
      -ldflags="-s -w -X main.releaseVersion=${VERSION} -X main.buildTime=${BUILD_TIME}" \
      -o /bin/api ./cmd/api

# ---- Stage 2: runtime ------------------------------------------------------
# distroless/static has CA certificates (PayHero and Africa's Talking are
# reached over HTTPS) and nothing else: no shell, no package manager.
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=builder /bin/api /api

EXPOSE 4000
USER nonroot:nonroot
ENTRYPOINT ["/api"]
