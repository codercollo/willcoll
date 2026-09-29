# Willcoll — CLAUDE.md

Persistent context primer for Claude Code sessions in this repo. Read this
first; it points to the docs that are the actual source of truth and records
decisions/conventions that aren't obvious from the code alone.

## What this project is

Willcoll is a multi-tenant property/rent reconciliation SaaS for Kenyan
management firms. It digitizes four paper documents a real firm (studied
directly) already produces by hand: the meter-reading ledger book, the
water/garbage bill slip, the KIWI PLACE receipt book, and the monthly
"ALL IN ONE PAYMENTS SCHEDULE" report. Everything in the domain model exists
to fill those four documents in automatically instead of by hand.

## Source-of-truth docs — read these, don't re-derive

- `system-design.txt` (repo root; the `docs/` and `backend/docs/` copies are
  currently empty placeholders — read the root one) —
  full domain model, DB schema, API design, RLS/concurrency rules, PayHero
  integration, reconciliation logic. This is the spec. When in doubt, this
  file wins over inference from partial code.
- `project-structure.txt` (repo root) — the exact target file tree for both
  `backend/` and `frontend/`. Before adding a new file, check whether it's
  already named here — match that name/location rather than inventing one.
- `design-tokens.txt` — the single source of truth for all frontend colors,
  spacing, typography, radii, component specs. Nothing in `frontend/` should
  hardcode a hex value, px spacing, or font-size outside these tokens.

## Build order

The backend is being built in stages, top-down through
`docs/system-design.txt` section 4 (API design), roughly following the
"Let's Go Further" (Greenlight) chapter order, adapted to this project's
actual domain and stack. Progress so far:

- **Stage 1 (done)** — `cmd/api/main.go`, `config.go`, `server.go`,
  `routes.go`, `healthcheck.go`, `Makefile`. Config-via-flags-defaulting-to-
  env-vars, `http.Server` with timeouts + graceful shutdown, Gin router with
  `/v1/healthcheck` wired and the full domain route map stubbed as comments
  pointing at the system-design.txt section that specifies each group.
- **Stage 2 (done)** — `cmd/api/helpers.go` (`envelope`, `writeJSON`,
  `readJSON`, `readIDParam`, `background`), `cmd/api/errors.go` (centralized
  error responses), `internal/validator/validator.go` (hand-rolled
  `Validator{Errors}` for business-rule checks; go-playground/validator
  struct tags handle mechanical checks).
- **Greenlight ch.3–8 (done)** — money/period types (`pkg/moneyfmt`),
  struct-tag + Validator validation, migrations 000001–000020 (roles,
  RLS, property archiving, security_invoker balances view), sqlc
  repository layer (`internal/db`: queries, `Store.ExecTenantTx`, gomock
  mocks via `make sqlc/generate`), `internal/data` service layer for every
  aggregate (version-checked updates, configurable `DB_QUERY_TIMEOUT`,
  append-only guard test), and the support files: SCS sessions + flash,
  authenticate / requireActivatedManager / requireAdmin, CORS, expvar
  metrics, trusted-proxy rate limiting.
- **Phases 3-7 (done)** — auth handlers; rent/water/garbage runs (all
  synchronous, one ledger transaction each); reports/receipts/invoice PDFs
  (`internal/pdf`, rendered on demand, **no blob storage**); PayHero
  collections + subscriptions webhooks and the reconciliation engine
  (`internal/reconciliation`, `data.CollectionsModel`); the public OTP-gated
  `/v1/pay` surface; manager billing and the payment review queue. Decisions
  worth knowing are in `backend/docs/adr/0004-*.md`.
- **Still stubbed** — `POST /v1/properties/:id/units/import` (bulk CSV).
- **Before go-live** — verify the `internal/payhero` request and callback
  shapes against a live PayHero account (isolated in `client.go` and
  `webhook.go`), and seed `subscription_plans` (admin role only).

When asked to continue the build, follow the stage order in the task rather
than jumping ahead.

## Stack & conventions

- **Go + Gin**, not `httprouter` — `system-design.txt` §4.1/4.2 specifies
  Gin explicitly (middleware chain, `*gin.Context` handlers). Don't
  reintroduce httprouter even though the generic Greenlight book uses it.
- **Handler signature**: `func (app *application) xHandler(c *gin.Context)`.
  Use `app.writeJSON` / `app.readJSON` / the `errors.go` responders, not
  `c.JSON(...)` directly — keeps the envelope format and error handling
  consistent project-wide.
- **No Docker / no air in local dev.** Development is "just Go, just
  Postgres, a Makefile" — run the API with `make run/api` (plain `go run`),
  not a file-watcher. Docker is Staging/Production only
  (`build/Dockerfile`, `build/docker-compose.*.yml`). Do not add
  `.air.toml` or wire hot-reload tooling into the dev loop.
- **SCS sessions, not JWT/PASETO** for the manager/admin dashboards (see
  `backend/docs/adr/0001-scs-sessions-over-jwt.md`) — sessions need to be
  instantly revocable when a subscription lapses.
- **Ledger tables are append-only.** Never `UPDATE`/`DELETE` `charges` or
  `ledger_entries`. Corrections are storno reversals (see
  `backend/docs/adr/0002-append-only-ledger-storno.md`). Balances are never
  a stored column — always derived via `unit_ledger_balances`.
- **Multi-tenancy is enforced at the DB layer via RLS**
  (`backend/docs/adr/0003-rls-tenant-isolation.md`), keyed on
  `SET LOCAL app.tenant_id`, set as the first statement in every
  tenant-scoped request's transaction. Never rely on application-layer
  filtering alone for tenant isolation.
- **Money**: `numeric(12,2)` in Postgres; format/parse through
  `pkg/moneyfmt`, never ad-hoc string formatting.
- **IDs** are UUIDs (`github.com/google/uuid`).

## Commands

Run from `backend/`:

- `make run/api` — run the API (plain `go run ./cmd/api`, no hot reload)
- `make build/api` — static binary (`CGO_ENABLED=0`) to `./bin/api` with the
  version embedded (`-X main.releaseVersion` / `main.buildTime`); reported by
  `/v1/healthcheck` and `/debug/vars` together with the VCS revision (`internal/vcs`)
- `make test` — `go test -race ./...` (the race detector needs cgo: with no C
  compiler, e.g. bare Windows, run `make test RACE=`)
- `make audit` — tidy, `go vet`, staticcheck, golangci-lint
  (`backend/.golangci.yml`), then `make test`
- `make test/integration` — every test, including the Postgres integration and
  concurrency tests, on a throwaway `willcoll_verify` database (dev DB untouched)
- `make tidy` — `go fmt` + `go mod tidy`
- `make db/roles` — one-time per environment: create/update the
  `willcoll_app` (API, subject to RLS) and `willcoll_admin` (BYPASSRLS)
  roles from `DB_APP_PASSWORD` / `DB_ADMIN_PASSWORD`
- `make db/migrations/up|down` — run as `MIGRATE_DSN` (owner/superuser)

**Two DSNs, never swapped:** the API uses `DB_DSN` (`willcoll_app`);
migrations use `MIGRATE_DSN`. Superusers bypass RLS even under FORCE ROW
LEVEL SECURITY, so pointing the API at the migration role silently
disables tenant isolation. `willcoll_app` also has no UPDATE/DELETE on
`charges`, `transaction_headers`, `ledger_entries`, `payment_allocations`
(migration 000018) — a new append-only table must revoke them in its own
migration.

## Git / commit policy

- `includeCoAuthoredBy` is disabled globally — do not add
  "Generated with Claude Code" / "Co-Authored-By: Claude" trailers.
- `git commit` and `git push` are globally denied for this tool — commits
  and pushes are the user's to run themselves.

## Housekeeping notes

- `backend/.env` was removed from git tracking (kept on disk, now
  git-ignored) — it held local secrets and should never be committed.
  Real config templates live in `backend/configs/config.example.env`.
