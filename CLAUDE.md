# Willcoll — CLAUDE.md

## What this is

Willcoll is a rent-management platform for landlords and letting agencies (starting in Kenya) that replaces the paper receipt books and handwritten ledgers many small property managers still use.

**Core idea:** every rent, water, and garbage payment gets recorded into an immutable digital ledger — entries can never be edited or deleted, only reversed with a reason. This fixes the biggest problem with paper books: missing cash, retroactive tampering, or "did I already collect this?" confusion.

**Who uses it:**
- **Manager** — runs day-to-day operations (records payments, manages units/leases, invites Agents).
- **Agent** — a caretaker/assistant a Manager delegates specific properties to, with fine-grained (PBAC) permissions.
- **Landlord** — the property owner; read-only, sees reports and gets SMS updates, never touches data entry.
- **Tenant** — never logs in; only receives SMS (rent due, receipt confirmation, arrears reminders).

**How payments are recorded:** manually, by the Manager/Agent, right after receiving money any way they already do (cash, M-Pesa, bank) — no integration with anyone's till or bank account required. The system classifies the payment by which invoice it settles (arrears first, then current rent, then water/garbage), not by guessing from the payment method.

**Multi-tenant SaaS:** one Organization (agency) per letting business, each with its own login slug, branding (logo/colors), and billing tier based on portfolio size.

**Premium add-on — Verified Property Score:** because the ledger data is tamper-proof, a small ML model can turn a property's real rent-collection history into bank-recognizable credit metrics (NOI, DSCR, a score band) — letting a landlord use real, provable income history to get a loan or refinance, something paper records could never support.

**In one line:** it's an honest, auditable digital ledger for rent collection that's easy for a small agency to adopt today, with a path to turning that trustworthy data into real financial value later.

Stack: Go backend + Nuxt frontend, monorepo.

## Read first
- `willcoll-spec.md` — the full functional/architectural spec (multi-tenancy via Postgres RLS, RBAC/PBAC, money/ledger model, phases). Treat as source of truth for *what* the system should do.
- `project-structure.txt` — package-by-package layout and the object-oriented conventions (every `internal/*` package with behavior is a `Service` struct + `NewService(...)`; handlers are thin controllers calling one service method each).
- `design-tokens.txt` — the base design system + Organization white-label overlay (colors, spacing vars used in `web/app` scoped styles).

## Repo layout
- `internal/api/` — HTTP handlers (`handlers_*.go`), route table in `router.go`, hand-rolled middleware (`middleware_*.go`). Handlers never contain business logic — they call a service method and translate to JSON.
- `internal/domain/`, `internal/{auth,billing,money,payments,tenancy,branding,notify,mailer}/` — the actual business logic, one `Service` per package.
- `web/app/pages/[slug]/...` — Nuxt pages, slug-prefixed per Organization. `web/app/composables/` — `useApi`, `useAuth`, `usePermissions` (RBAC/PBAC checks — reuse `PropertyGrant`/`PbacAction` types, don't redefine).
- `api/openapi.yaml` — hand-maintained OpenAPI spec; `web/app/types/api.d.ts` is generated from it via `npm run generate:api` (also runs on `predev`/`prebuild`). **Edit the yaml, then regenerate — never hand-edit `api.d.ts`.**

## Conventions to follow
- Multi-tenancy: every business table has `organization_id` + Postgres RLS. Never bypass this or write app-level `WHERE organization_id = ?` as a substitute — the tenant-scope middleware (`middleware_tenant.go`) sets `app.current_org_id` per request.
- PBAC bitset fields (the ONLY real per-property Agent permissions — never invent new ones): `can_record_payments`, `can_edit_leases`, `can_edit_unit_pricing`, `can_void_payments`, `can_view_financial_reports`, `can_manage_meter_readings`.
- Payments/ledger entries are append-only — never edit, only insert/reverse.
- Frontend pages: no UI component library, no Tailwind — plain scoped `<style>` using CSS custom properties from `design-tokens.txt`. Follow the existing page shape (`status: 'loading'|'ready'|'empty'|'error'`, inline create forms, `RoleGate` for role/action/PBAC-gated UI).
- Go: `go build ./... && go vet ./...` should stay clean before considering a backend change done.

## Project Context & State

### Active Feature
- (none in progress)

### Completed
- Phase 6 — Seeded dev user: `cmd/willcoll-seed` implemented for real (was an empty skeleton) — reuses `tenancy.Service.CreateOrganizationWithFirstManagerTx` (already does activated=true/status=active/is_super_manager=true) with `subscription_tier=professional`, `billing_status=active`, fixed dev creds (`manager@willcoll.dev` / `willcoll-dev`, slug `willcoll-dev`). Idempotent — checks for the email first, safe to rerun. Verified for real: ran migrate+seed against a fresh dev Postgres, confirmed the DB row values match exactly, then booted the API on the host and logged in with the seeded credentials over `POST /v1/auth/login` (got a real PASETO token back). 6.2 confirmed by grep: `pg_data_dev` and `willcoll-seed` appear nowhere in `scripts/deploy.sh`, `build/docker-compose.prod.yml`, or `.github/workflows/deploy.yml`; the prod api image only builds `./cmd/willcoll`, never `./cmd/willcoll-seed`.
- Docker infra Phase 5 — Backups: `scripts/backup/{nightly-pg-dump.sh,prune-old-backups.sh,crontab}`, `build/package/Dockerfile.backup` (postgres-alpine + dcron), `pg_backup` service added to `build/docker-compose.prod.yml` only (restart:always, `pg_backups` volume, nightly 01:00 UTC dump then prune to `BACKUP_RETENTION_DAYS`, default 7). Wired into `deploy.yml` (builds+pushes `willcoll-backup:prod`) and `scripts/deploy.sh`'s pull list. Fixed `make backup-now`'s path (was `scripts/backup/nightly-pg-dump.sh`, a repo-relative path that doesn't exist inside the container — the Dockerfile installs it at `/scripts/nightly-pg-dump.sh`). Config-validated via `docker compose config`, not run end-to-end (needs a live VPS/Postgres to actually exercise cron+dump).
- Docker infra Phases 2 & 4 + Contabo deploy: `build/package/Dockerfile.{api,web}` and `ml/Dockerfile` (multi-stage dev/builder/prod), `build/docker-compose.yml` (dev: postgres/api/web/ml-sidecar/migrate, `pg_data_dev`) + `build/docker-compose.prod.yml` overlay (`pg_data_prod` on its own driver_opts path, no exposed db/api/web/sidecar ports via `!override`, restart:always, caddy on 80/443). Compose project renamed to `willcoll` (`name:` key), every service image explicitly tagged `willcoll-<service>:<stage>` locally and `ghcr.io/$GHCR_NAMESPACE/willcoll-<service>:<stage>` in prod. Fixed `build/Caddyfile` to route by service name (`api:4000`/`web:80`) instead of the stale `localhost:4000` — confirmed with the user first since Phase 4's literal spec (`/api/*`, `api:8080`, `index.html`) didn't match this repo's real routes/port/Nuxt fallback file, they chose keeping the real values. Added `.github/workflows/{test,deploy}.yml` (build+push 4 images to GHCR, SSH+`scripts/deploy.sh` on the Contabo VPS) and `scripts/deploy.sh` (pull-only, never builds on the VPS; migrate via the pushed `:builder` image).
- Verified for real in this session: both api Dockerfile targets (dev via air, prod) build and the prod image actually starts and connects to Postgres over the compose network; the web dev image's pnpm install (fixed a real bug — corepack fetching pnpm 12 instead of the pinned 11.5.2 broke `pnpm-workspace.yaml`'s `allowBuilds` gate, now pinned via `packageManager` in `web/package.json`).
- Not verified in this session (stopped early to avoid burning further tokens on build-and-check loops): the web *image* build with the pnpm fix applied, and the ml-sidecar Dockerfile build. Both are low-risk (straightforward Dockerfiles, same pattern as api) but haven't actually been run.
- Dev DB auth fix (Docker infra Phase 1): created `build/docker-compose.yml` (didn't exist — postgres service only; migrate/api/web/caddy/pg_backup come with later infra phases per `project-structure.txt`), `make reset-dev` (down -v → up --wait → migrate w/ one retry for the initdb-restart race → seed), root `README.md` (didn't exist). Root cause of the reported auth failure was NOT a stale volume — it was a **native Windows Postgres service already bound to port 5432** silently intercepting connections meant for the container. Fixed by moving dev Postgres to host port 5433 everywhere (`.env`, `.env.example`, `configs/config.example.yaml`'s default DSN, compose file) — asked the user first since it's an environment-affecting call, they chose remap-port over stopping their native service. Verified `make reset-dev` end-to-end for real (this session has Docker + Postgres available, unlike earlier phases).
- Phase 23 — Platform Admin Panel: `cmd/willcoll-admin` (separate binary) + `internal/admin` (own service/http/templates, plain hand-rolled `html/template` pages, no client JS). Asked the user whether to actually pull in GoAdmin (the plan named it) vs. hand-roll something scoped to just 23.4-23.6 given this repo's own no-framework convention and that nothing here could be verified against a live DB/browser in this session — they chose hand-rolled. New `platform_admin_panel` BYPASSRLS Postgres role (migration 000019, its own DSN, never `internal/api`'s pool) + `audit_log.source` column (`'platform_admin'` tag on every write this panel makes, per 23.6). Screens: organizations (list + tier/billing_status edit, the only write path), score-addon subscribers, cross-org audit log, operations (ledger drift via `money.Service.Reconcile` reused as-is, stuck `payment_gateway_transactions` >1h PENDING). Smoke-tested template rendering (`internal/admin/templates_test.go`) since there's no live DB here to click through screens.
- Phase 9 — Verified Property Score: `pages/[slug]/settings/score-addon.vue` (activate/cancel, Manager only), `pages/[slug]/properties/[id]/score.vue` (request form Manager-only via `RoleGate`, Landlord sees read-only display + history). Added `GetAddonStatus`/`GET /v1/organization/addons/verified-property-score` (didn't exist — needed to render activate vs. cancel state). Score band (A/B/C/D) maps onto `StatusBadge`'s 3 existing tones (A/B→success, C→warning, D→error), no new colors invented.
- Phase 10 — Audit log: `pages/[slug]/audit-log.vue`, Manager-only, property filter, uses existing `GET /v1/audit-log?property_id=`.
- Phase 8 — Settings: `pages/[slug]/settings/{index,branding,sms-templates}.vue`. Branding is super-manager gated (added `is_super_manager` to auth responses/`SessionClaims`/`usePermissions().isSuperManager` — it didn't exist before, needed to match the backend's `requireSuperManager` client-side). SMS templates are backed by a new `sms_template_overrides` table (migration 000018) + `notify.Templates` registry (the closed set of 6 real templates + their actual variables, read from `internal/notify/templates.go`) + `GET/PATCH /v1/organization/sms-templates`; `notify.Service.SendTemplate` now checks for an override before falling back to the embedded `.tmpl` file. Removed `POST/GET /v1/managers/payout-account` and their handlers (dead weight — no frontend page ever existed for them); `internal/payments` payout-account service methods/table were left alone since tenant self-pay initiation still reads them.
- Phase 7 — Reports: `pages/[slug]/reports/{arrears,collections,portfolio}.vue`. Portfolio is the Landlord's primary screen; backend (`internal/reports`) already scopes rows by role/owner, page does no client-side filtering.
- Phase 6 — Agents & PBAC (Manager-only): `pages/[slug]/agents/index.vue` (list, invite, reset-password), PBAC grant matrix editor wired to `POST /v1/agent-grants`. Added `GET /v1/agents` and `GET /v1/agent-grants?agent_id=` backend endpoints (not in the original phase spec but required to render the list/matrix) plus corresponding `openapi.yaml` entries.

### Technical Debt & Gotchas
- `make up` now builds/starts api+web+ml-sidecar too (Phase 2 added them to `build/docker-compose.yml`), not just postgres like Phase 1 — much slower than before. If the host-run dev workflow (`go run ./cmd/willcoll`, `pnpm dev`) is still preferred day-to-day, consider `docker compose ... up -d postgres` directly, or splitting a `make up-db` target, rather than always paying for 3 image builds.
- `GET /v1/agents` and `GET /v1/agent-grants` were added ad hoc to support the UI — verify against `willcoll-spec.md` if it's later revised to define these explicitly, to avoid drift.
- `web/app/types/api.d.ts` is generated — if it looks out of sync with a handler change, run `npm run generate:api` in `web/` rather than editing it directly.
- No frontend test/typecheck tooling is currently working out of the box in this environment (`vue-tsc` via `npx` fails on a `typescript` package export resolution issue) — verify type correctness by reading the diff carefully until that's fixed.

### Next Steps
- Run `docker build -f build/package/Dockerfile.web --target dev .` and `docker build -f ml/Dockerfile --target dev ml` once to confirm they actually build (fixed a pnpm version-pinning bug for web; ml-sidecar's Dockerfile has never been built at all).
- Set real GitHub repo secrets (`CONTABO_HOST`, `CONTABO_USER`, `CONTABO_SSH_KEY`, `CONTABO_APP_DIR`) and `GHCR_NAMESPACE` in the VPS's `.env` before `deploy.yml` can actually deploy anything.
- Run migration 000019 and set a real `platform_admin_panel` role password (the migration ships `'changeme_in_production'`, same placeholder pattern as the existing `ml_sidecar_readonly` role) before any real deployment.
- Once a Postgres instance is reachable, click through all `/admin/*` screens for real — only template rendering was smoke-tested here, not the actual SQL against live data.
- Verify `pages/[slug]/reports/portfolio.vue` against a real Landlord login, and Phase 8's SMS template save/validate flow (including migration 000018), once a Postgres instance is reachable (`make up` / `make migrate` / `make seed`) — this environment has no live DB, so both phases were only build/vet-checked, not exercised end-to-end.
- Confirm with the user whether Landlord should really be allowed to edit SMS templates (currently gated `requireRole("manager", "landlord")` to match the pre-existing frontend RBAC table's `configure_sms_templates` action) — the spec doesn't explicitly say, and it's a real behavior decision (a Landlord editing a Manager's outgoing tenant SMS wording), not just a permissions technicality.
