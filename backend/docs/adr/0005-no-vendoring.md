# 0005. Dependencies are not vendored

Status: accepted

## Context

`vendor/` (committed with `go mod vendor`, built with `-mod=vendor`) removes the
build's dependence on a module proxy, which helps when the machine building
the release has flaky access to the registry. The build docs suggest it for a
small team that deploys from a VPS.

## Decision

Willcoll does **not** vendor. Nothing is deployed by building on the VPS:

- Images are built in CI (`.github/workflows`), where the proxy is reliable,
  from `build/Dockerfile`, and the VPS only pulls the finished image.
- The Dockerfile downloads modules in their own layer keyed on `go.mod` and
  `go.sum`, then runs `go mod verify`, so a build re-downloads only when
  dependencies change, and a tampered module fails the build.
- Reproducibility comes from `go.sum` (content hashes, checked by the Go
  toolchain and by the `go mod tidy` / `go mod verify` steps in CI) rather
  than from copies of the source.
- A vendor tree would add tens of megabytes of third-party code to every
  diff and code search, for a risk the deployment model already removes.

## Consequences

- A build needs network access to `proxy.golang.org` (or `GOPROXY`).
- If releases ever have to be built where that is unreliable, or a dependency
  is at risk of disappearing, revisit: `go mod vendor`, commit `vendor/`, set
  `GOFLAGS=-mod=vendor` in the Dockerfile builder stage, and add a CI check
  that `go mod vendor` leaves no diff. Nothing else changes.
