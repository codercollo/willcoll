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

- `docs/system-design.txt` and `docs/project-structure.txt` (repo root) —
  the canonical current-state + target-state docs for the platform and its
  three products (Rentals shipped, Estates and Waste to build). When in
  doubt, these files win over inference from partial code.
- `docs/rentals/features-functionalities.txt` — Rentals endpoint index for
  both APIs (written in 3i-c from the routers; keep it in sync with
  `cmd/rentals-api/routes.go` and `cmd/platform-api/routes.go`).
- `design-tokens.txt` — the single source of truth for all frontend colors,
  spacing, typography, radii, component specs. Nothing in `frontend/` should
  hardcode a hex value, px spacing, or font-size outside these tokens.

## Build order

**Plan change**: there is no staging server and no live production data — a
laptop is "staging" until one VPS can be afforded for production. Phase 3
now finishes entirely against local databases (3i included); Phase 4 is
local-only CI + a local-staging Docker Compose stack (`make staging/up`),
not a real deploy; Phases 5-8 build and verify against that local stack; the
actual VPS, deploy workflows and first real customer move to a new Phase 9
("Go live"), done once, at the end. Full detail in
`docs/build-phases.txt`'s own top-of-file note and Phase 4/9 sections.

The backend was built in stages, top-down through `docs/system-design.txt`'s
API design section, roughly following the "Let's Go Further" (Greenlight)
chapter order, adapted to this project's actual domain and stack. Progress
so far:

- **Stage 1 (done)** — `main.go`, `config.go`, `server.go`,
  `routes.go`, `healthcheck.go`, `Makefile` (built under `cmd/api/` at the
  time; that directory is now `cmd/rentals-api/`, see Phase 2 below).
- **Stage 2 (done)** — `helpers.go`, `errors.go`,
  `internal/shared/validator/validator.go`.
- **Greenlight ch.3–8 (done)** — money/period types (`pkg/moneyfmt`),
  struct-tag + Validator validation, migrations 000001–000044, sqlc
  repository layer, `internal/rentals/data` service layer, SCS sessions,
  authenticate / requireActivatedManager / requireAdmin, CORS, expvar
  metrics, trusted-proxy rate limiting.
- **Phases 3-7 (done)** — auth handlers; rent/water/garbage/electricity
  runs; reports/receipts/invoice PDFs (`internal/rentals/pdf`, rendered on demand,
  **no blob storage**); PayHero collections + subscriptions webhooks and
  the reconciliation engine; the public OTP-gated `/v1/pay` surface;
  manager billing and the payment review queue.
- **Phase 1 of the three-product split (done)** — `docs/build-phases.txt`
  Phase 1 / `docs/project-structure.txt` Part D step 1: pure extraction of
  product-agnostic code into `internal/shared/`, no behavior change. All of
  1a-1i landed: leaf packages (vcs, jsonlog, validator, sms, mailer,
  payhero) -> reconcile -> pdf split -> data/db move (`internal/rentals/`)
  -> httpx -> ledgerkit (partial — see below) -> auth -> jobs -> exportx ->
  the `internal/shared` no-product-import guard (depguard +
  `internal/shared/depguard_test.go`).
- **Phase 2 of the three-product split (done)** — `docs/build-phases.txt`
  Phase 2: `cmd/api` renamed to `cmd/rentals-api`, mechanical only.
  `Makefile` gained `run/rentals-api`/`build/rentals-api` (with `run/api`/
  `build/api` kept as deprecated aliases), `build/Dockerfile` now takes
  `ARG SERVICE` (default `rentals-api`) and builds `./cmd/${SERVICE}`.
- **Phase 3 of the three-product split (in progress)** — `docs/
  build-phases.txt` Phase 3 steps 3a-3e done except one human decision:
  ADR-0006/0007; `migrations/platform/` (14 migrations incl.
  `module_usage`), `internal/platform/{data,db}` (identity core, plan
  CRUD, usage/pricing logic, `ModuleModel` for manager_modules/
  per-product subscriptions); `cmd/platform-api` stood up with
  signup/activate/sessions/tokens/account/admin-plan-CRUD moved over
  (same shapes, `rentals-api`'s own copies NOT yet removed — that's 3i's
  local cut-over now, there being no staging/production server to defer it
  to — see `docs/build-phases.txt`'s top-of-file plan change); `cmd/
  devtools backfill-platform`
  copies identity/billing rows into `willcoll_platform` and seeds
  `module_usage` + `manager_modules` (rentals, active) for every existing
  manager. `rentals-api` reports its own unit count into `module_usage`
  on unit create/property delete/bulk import plus a daily recount job,
  and now runs `requireModule("rentals")`/`requirePlatformSubscription
  ("rentals")` on every manager route behind `GATE_MODE=shadow|enforce`
  (config.go, default `shadow` — logs a would-be 403/402 and any
  disagreement with today's access logic, never blocks). `PLATFORM_DB_DSN`
  is optional only in development; staging/production fail startup
  without it. The Super Admin health page reports `usage_reporting:
  {enabled, last_report_at}`. Verified live end to end, including
  `GATE_MODE=enforce` actually 403'ing a suspended module and a
  re-enable instantly restoring access with data intact.
  `requireRole` is NOT built — no staff/role concept exists yet
  (Phase 3f); it lands there, once there's a real principal to gate on.
  **3c-iv-b1 and b2 are done**: `StartRenewal`/`Plans`/`SetPlanForDev`/the
  subscription webhook now live in `internal/platform/data` (priced from
  `module_usage`, never a live cross-database query — see
  `docs/system-design.txt` A.7's DECISION), served from `platform-api`,
  and `rentals-api`'s own copies of every billing handler (plans,
  subscription, renew, invoices, invoice PDF, the webhook) delegate to
  them over `app.platform`. `requireSubscription()`/the `access` key also
  read from `willcoll_platform` now, so a renewal unlocks access at once.
  `rentals-api` reads and writes no subscription rows in `willcoll_rentals`
  any more; `internal/rentals/data/billing.go` is dead code except for
  `cmd/devtools`' still-unmoved `expire-trial`/`reset-trial` (a 3i cleanup
  item, same as `dev/test-plan` still seeding the OLD rentals plan
  catalog — `dev/set-plan` was moved to platform in b2, so a plan named
  for it must exist in platform's catalog instead). **3c-iv-b3 is done**:
  the backfill's `subscriptions`/`subscription_invoices` copies are now
  UPSERTs (newer wins by `version`/`paid_at`), verified live with a real
  re-run; the cutover checklist is in `docs/build-phases.txt`. **3f-1
  (staff accounts) is done**: `internal/platform/data.StaffModel`, staff
  as a second session principal (`cmd/platform-api`'s `authenticate()`
  resolves a staff session to both the staff row and their manager, so
  every existing tenant/RLS check keeps working unchanged), `requireOwner()`
  gating `/v1/staff/*` and `/v1/billing/*`. Two migrations landed with it:
  000015 (`staff.status` gains invited/disabled) and 000016 (grants
  `willcoll_platform_admin` UPDATE on `staff` — a live-testing catch, the
  same class of gap as the subscriptions/module_usage grants in 000013).
  **3f-2 is done**: `requireRole`/`requireStaffModuleAccess` in
  `cmd/rentals-api/roles.go` (`routeMinRole` table, unlisted routes
  default to viewer-for-GET/owner-for-writes). This surfaced two real
  gaps, both fixed: rentals-api had no way to see a staff session at all
  (it verifies its own manager/admin logins against its own local DB,
  never `willcoll_platform`) — fixed with a read-only second
  `scs.SessionManager` bound to platform (`tryStaffSession`, only ever
  `Load`, never `RenewToken`/`Commit`/`Destroy`; rentals-api's own cookie
  renamed `willcoll_rentals_session` so it can't collide with platform's
  `willcoll_session`). And `app.platform`'s `AdminStore` (BYPASSRLS) was
  wired to the app-role connection only, so the new staff lookup — and,
  latently since 3c-iv-b1, the subscription webhook's cross-tenant
  invoice resolution — errored with "unrecognized configuration
  parameter app.tenant_id"; fixed with a real `PLATFORM_DB_ADMIN_DSN`
  (same derive-from-password pattern as the local admin DSN), wired into
  `Staff.AdminStore`/`Billing.AdminStore` only (everything else keeps its
  RLS defense-in-depth). Verified live end to end (officer via
  platform-api reaching rentals-api, role/module_access/disabled-staff
  gating all correct). **3f-3 is done**: trials moved from
  `managers.trial_ends_at` to `manager_modules.trial_ends_at` (migration
  000017, backfilled). `ModuleModel.StartTrial` starts one once, per
  (manager, product) — a manager whose Rentals trial expired still gets a
  fresh, independent Estates trial. Activation
  (`cmd/platform-api/managers.go`) calls it instead of touching the
  manager row; `accessForManager`/`requirePlatformSubscription` in
  rentals-api read the module's trial now, not the platform manager's.
  `managers.trial_ends_at` itself stays (dropped later, once nothing
  reads it) — rentals-api's own dev-only local fallback (no
  `PLATFORM_DB_DSN`) still uses its own copy, since Rentals' database has
  no per-product concept to fall back to. **3f-4 (audit log) is done,
  Phase 3f is complete.** `audit_log` gained `actor_type`
  (manager|staff|admin|system) + `actor_id` (migration 000018) — the
  manager's own action now reads as "manager", never blank.
  `app.audit(...)`/`app.auditAdmin(...)` in each binary fire in the
  background and count failures (`auditFailureTracker`, trailing 24h),
  surfaced as `audit_failures_24h` on rentals-api's health page (the one
  Super Admin health page that exists; platform-api has none yet).
  Coverage is enforced, not best-effort:
  `cmd/{rentals,platform}-api/audit_coverage_test.go` walks every
  registered write route and fails if it's missing from a maintained
  `auditCoverage` map ("audited", source-verified to actually call
  `app.audit`/`app.auditAdmin`, or "exempt: reason") — a new write route
  with neither fails the build. Every write route in both binaries is
  now classified; Estates/Waste inherit the same file-per-binary pattern.
  **3f-5 (done)** — one business calendar, Africa/Nairobi: a Postgres
  DATE column scans into Go at midnight UTC, and comparing that against a
  server-zoned `time.Now()` silently shifted trial/subscription expiry by
  up to 3 hours around every midnight. `pkg/moneyfmt/clock.go` adds
  `Now()`/`Today()` (Nairobi, injectable via `NowFunc` for tests) and
  `AtNairobiMidnight()` (re-anchors a DATE-scanned value before day-
  boundary arithmetic). Every calendar-relevant `time.Now()` in
  `internal/{rentals,platform}/data` and both APIs now goes through it —
  see `docs/build-phases.txt` 3f-5 for the full file list and what was
  deliberately left alone (`Period.FirstDay()`'s UTC write path; pure
  elapsed-duration/TTL bookkeeping).
  Separately, the only thing left in Phase
  3's earlier 3e-b step is a **human decision, not code**: with no staging
  server to show "zero mismatches" on, 3e-b step d now reads the shadow-
  mismatch log from 3i's local rehearsal script instead, then the user
  (never the agent, never by editing code) flips the `GATE_MODE=enforce`
  default in config.go once that rehearsal is clean.
  **3g-1 (payment_channels) is done**: `internal/platform/data.ChannelModel`
  (Register/GetByOwner/Get over the existing `payment_channels` table —
  migration 000010 already had channel_id as its PK, the actual
  one-owner-per-channel guard). `registerPaymentChannelHandler` claims the
  channel in `willcoll_platform` BEFORE saving it on the local property —
  a claim conflict is 409 and never touches the property row.
  `cmd/devtools backfill-platform` now also registers every property's
  existing `payhero_channel_id`, idempotent, surfacing any channel id
  claimed by two different properties as a named duplicate rather than
  silently picking a winner. See `docs/build-phases.txt` 3g-1 for the
  mockgen-version workaround (hand-added `Querier` methods) and full file
  list. **3g-2 (inbox claim/post) is done**: `InboxModel`
  (`internal/platform/data/payment_inbox.go`) claims a successful
  collection in `willcoll_platform.payment_inbox` BEFORE
  `processCollections` runs its existing local routing/posting pipeline
  (now `postCollectionsLocally`, unchanged, just extracted) — a claim
  conflict under a different product is rejected and alerted, already-
  posted is acked without reprocessing, and platform being unreachable
  falls back to local-only posting (counted as `inbox_fallbacks_24h` on
  the health page) so collections never stop. See `docs/build-phases.txt`
  3g-2 for a real gomock pitfall hit while testing this (expectation-order
  shadowing) and a sandbox-only `test/integration` parallelism caveat
  (`-p 2`) unrelated to correctness. **3g-3 (sweeper) is done, 3g is
  complete**: `startInboxSweeperJob` (wired in `server.go` next to the
  existing usage-recount job) re-drives every `payment_inbox` row still
  `claimed` more than 5 minutes old through the SAME `processCollections`
  function a live webhook delivery uses — deliberately one code path for
  "first delivery," "PayHero's own retry," and "the sweeper, minutes
  later," with the local pipeline's own `mpesa_receipt` uniqueness as the
  one guard all three lean on. **3i-a (schema migration), 3i-b and 3i-c are
  done**: `migrations/` renamed to `migrations/rentals/` (git mv, version
  numbers UNCHANGED — renumbering would desync every real environment's
  `schema_migrations` table for no benefit, so "old->new" ended up meaning
  a path change, not new numbers; documented as that judgment call in
  `docs/build-phases.txt` 3i-a). New migration
  `000045_platform_split_drop_local_identity`: drops every FK referencing
  `managers` (found generically via `pg_constraint`, not hand-listed) then
  drops `subscription_invoices`/`subscriptions`/`subscription_plans`/
  `tokens`/`admins`/`managers` per ADR-0006; its `.down.sql` deliberately
  `RAISE`s — the only real rollback is the pre-cutover `pg_dump`. Running
  the gate against a scratch DB with this migration applied surfaced ~13
  live `rentals-api` routes (its own registration/login/account/admin-
  manager management) and `TestTrialAndPaywallEndToEnd` still depending on
  the now-dropped tables — not a quick fixture fix, so this was surfaced to
  the user mid-sub-step rather than guessed at; their answer is 3i-b
  (retire that local identity subsystem, `authenticate()` resolves managers
  the same way 3f-2 already resolves staff, `GATE_MODE` defaults to
  enforce) and 3i-c (frontend points auth/billing at platform-api; the
  local rehearsal script). **3i-b is done**: `rentals-api` has no identity
  of its own any more — its registration/activation/login/password/
  account/billing/admin-manager/admin-plan routes, handlers, queries and
  `internal/rentals/data` models (managers, tokens, admins, billing,
  admin_plans, access) are deleted. `authenticate()` resolves manager,
  staff AND Super Admin from the willcoll_platform session (read-only,
  never mints one), reading their status from platform on every request;
  `PLATFORM_DB_DSN` is required in every environment. The platform gates
  always enforce (`GATE_MODE` and the shadow code are gone);
  `ComputeAccess` lives in `internal/platform/data/access.go`.
  `platform-api` gained the Super Admin oversight: `GET /v1/admin/managers
  [/:id]` (firm, modules, per-product subscriptions, staff_count,
  module_usage — no product DB reads, so no property_count), `POST
  .../suspend|reinstate` (suspend deletes ALL the manager's and their
  staff's sessions at once and audits as actor_type=admin; a suspended
  firm's staff can't sign in either), `GET /v1/admin/subscriptions`
  (product + status filters), all via `OversightModel` on the admin pool.
  `cmd/devtools` and `cmd/seed` now work against willcoll_platform
  (`PLATFORM_DSN`; seed `-reset` needs `PLATFORM_MIGRATE_DSN` too).
  **3i-c is done**: `make sqlc/generate` (scripts/generate_sqlc.sh) runs
  sqlc then `go run go.uber.org/mock/mockgen@v0.6.0` for every package —
  never hand-edit mocks, never use a global mockgen. `make
  test/integration` creates BOTH scratch databases (willcoll_verify and
  willcoll_platform_verify), exports INTEGRATION=1 plus four DSNs, and
  prints tests run/pass/fail/skip per package; with INTEGRATION=1 an
  integration test missing its DSN FAILS (`internal/testenv.Require`),
  so a green run means the database tests ran. The platform roles
  (willcoll_platform_app/_admin) are cluster-wide: `scripts/lib_testdb.sh`
  creates them with throwaway passwords only if absent, and wants
  PLATFORM_DB_APP_PASSWORD / PLATFORM_DB_ADMIN_PASSWORD in `.env` if they
  exist. `make rehearse/cutover` (scripts/rehearse_cutover.sh) rehearses the
  whole split from a fresh pre-split database through backfill, 000045 and
  rename to both REAL APIs and a 65-step HTTP smoke test (`cmd/rehearse`);
  keep it working as phases add migrations. The Rentals frontend sends auth/
  account/billing/admin-oversight calls to platform-api via
  `app/utils/apiTarget.ts` (`apiPlatformBase`, default the same-origin
  `/_platform` proxy) and has a real `pnpm lint`. **3i-d is done**: a
  hand-typed `payhero_channel_id` on POST/PATCH `/properties` is claimed in
  platform BEFORE the save (409 if owned elsewhere, 503 if platform is down,
  old claim released on change/clear, kept if the channel has recorded
  payments); the backfill runs with the willcoll_platform OWNER DSN — see
  build-phases 3i-d.
  **3k is done (shared frontend layer)**: `frontend/` is ONE pnpm
  workspace (`layers/*`, `rentals`; one lockfile at `frontend/`; root
  scripts lint/typecheck/test/build run across packages; estates/waste/
  platform scaffolds join in their own phases). `frontend/layers/base`
  (`@willcoll/base`, no app.vue/pages) holds ui/*, LedgerTable,
  SpreadsheetTable, OtpChallenge, PayIntentForm, useApi/useCurrency/
  usePeriod/useSubmit/useToast, `useAuthStore` (the platform session; a
  composable file, because @pinia/nuxt only scans the app's own stores/),
  money/period/date/apiTarget/routeGuard utils, the auth middleware, design
  tokens + main.css, and the shared eslint config. Per-product values come
  from the app's `runtimeConfig.public.product` and `.guard`
  (`rentals/guard.config.ts`, also asserted by a test); explicit imports
  of base files use the `#base/...` alias (declared in the layer's
  nuxt.config AND in each app's vitest.config). Base files import each other
  relatively, never `~`, which would resolve to the consuming app. The
  extraction itself changed no behaviour; the tests that moved with the code
  split billing.test/pay.test (134 tests before and after). Separately, in
  the same session, AppShell gained a mobile bottom tab bar (below md,
  "More" sheet for the rest + sign-out) and the UI went responsive
  (`stack-table` cards, bottom-sheet modals): a deliberate visual change on
  phones only; >= md is unchanged.
  **3l is done (frontend/platform)**: `frontend/platform` is the account and
  admin app (`app.willcoll.co.ke`; dev http://localhost:3001) and joined the
  workspace (`layers/*`, `platform`, `rentals`; the stand-alone lockfiles in
  estates/waste/platform were removed). It holds signup (Rentals on, Estates/
  Waste "coming soon"), activate, login, forgot/reset, staff invite
  acceptance (`/auth/activate-staff`), account, staff (owner only), modules,
  per-product billing (`/billing/[product]`), the Super Admin (login,
  managers, subscriptions, plans, system) and "/" = the firm's products as
  cards. These pages were MOVED out of rentals (same API calls). Rentals now
  has product pages only: any signed-out visit redirects (full page) to
  platform's `/auth/login?return_to=<rentals url>`; `return_to` is accepted
  only if its origin is in `runtimeConfig.public.apps` (dev: localhost:3001/
  3000; prod: subdomains via `NUXT_PUBLIC_APPS_*`), see
  `layers/base/app/utils/appLinks.ts` `safeReturnTo` (tested, rejects other
  origins, `//host`, backslashes, userinfo, other schemes). A product app's
  guard has `loginMode: 'platform'` and `routeGuard.platformRedirect` maps
  the login / admin-login / paywall ("/billing") targets to platform URLs;
  platform's guard is `'local'`. AppShell has a product switcher
  (`ui/ProductSwitcher`): modules from `GET /v1/modules` that are trialing/
  active, staff limited to `module_access`, plus Account/Billing links to
  platform; Billing is shown to owners only. The sliding-session pinger lives
  in the base layer so it runs in both apps; cookies: host-only `localhost`
  in dev (shared across ports), `SESSION_COOKIE_DOMAIN` in production
  (backend config, already existed). `pnpm dev` (scripts/dev.mjs) starts
  both apps; backend `scripts/lib_dev.sh` points platform-api's
  `FRONTEND_URL` (email links) at :3001 (`PLATFORM_FRONTEND_URL` overrides).
  **Phase 3 is done.** Close-out of 3l's gaps: staff change their own
  password at `PUT /v1/staff/me/password` (current + new, other sessions of
  theirs signed out, this one re-issued; `PUT /v1/account/password` now
  refuses a staff session); a firm with no properties is sent to the Rentals
  setup guide once per firm per tab (`rentals/app/utils/setupRedirect.ts`).
  Moved to later phases: SMS wallet page and audit log page -> Phase 5 (5k),
  "request activation" for Estates/Waste -> Phases 6a/7a (until then they
  show "coming soon").
  `docs/rentals/features-functionalities.txt` is the endpoint index (new in
  3i-c; the file CLAUDE.md used to mention never existed in git). See
  `docs/build-phases.txt` 3i-b/3i-c for every judgment call. CI/CD split,
  Estates, Waste have not started.
  CAUTION for anyone testing devtools locally: `scripts/db_roles*.sql`
  roles (`willcoll_app`, `willcoll_admin`, `willcoll_platform_app`,
  `willcoll_platform_admin`) are cluster-wide, not per-database — running
  one against a throwaway database still rewrites the SAME role used by
  the real dev database. Always re-apply the real passwords from `.env`
  afterward and confirm with `make test/integration`.

When asked to continue the build, follow the stage order in the task rather
than jumping ahead.

## Stack & conventions

- **Go + Gin**, not `httprouter`.
- **Handler signature**: `func (app *application) xHandler(c *gin.Context)`.
  Use `app.writeJSON` / `app.readJSON` / the `errors.go` responders, not
  `c.JSON(...)` directly.
- **No Docker / no air in local dev.** Development is "just Go, just
  Postgres, a Makefile" — run the API with `make run/rentals-api` (plain
  `go run`). Docker is Staging/Production only (`build/Dockerfile`). Do not
  add `.air.toml` or wire hot-reload tooling into the dev loop.
- **SCS sessions, not JWT/PASETO** for the manager/admin dashboards (see
  `docs/adr/0001-scs-sessions-over-jwt.md`) — sessions need to be instantly
  revocable when a subscription lapses.
- **Ledger tables are append-only.** Never `UPDATE`/`DELETE` `charges` or
  `ledger_entries`. Corrections are storno reversals (see
  `docs/adr/0002-append-only-ledger-storno.md`). Balances are never a
  stored column — always derived via `unit_ledger_balances`.
- **Multi-tenancy is enforced at the DB layer via RLS**
  (`docs/adr/0003-rls-tenant-isolation.md`), keyed on
  `SET LOCAL app.tenant_id`, set as the first statement in every
  tenant-scoped request's transaction. Never rely on application-layer
  filtering alone for tenant isolation.
- **Money**: `numeric(12,2)` in Postgres; format/parse through
  `pkg/moneyfmt`, never ad-hoc string formatting.
- **IDs** are UUIDs (`github.com/google/uuid`).

## Commands

Run from `backend/`:

- **Local dev stack** (`docs/dev-setup.md`): `make dev/setup` (idempotent: roles,
  `willcoll_rentals` + `willcoll_platform` databases, all migrations, demo firm
  via `cmd/seed`, `TEST - KES 10` plan, Super Admin), `make dev/up` (both APIs +
  local SMS/PayHero fakes, Ctrl+C stops all), `make run/platform-api`,
  `make run/rentals-api`; `pnpm dev` from `frontend/` (platform :3001 + rentals :3000). The dev scripts
  (`scripts/lib_dev.sh`) DERIVE their DSNs from `MIGRATE_DSN` + role passwords
  and never use `DB_DSN` as written, so the existing `willcoll` database is not
  touched (they refuse it without `CONFIRM_EXISTING_DB=yes`). `nuxt` is pinned
  to exactly 4.5.2: 4.6.0 returns 500 on every page in dev ("Either manifest or
  precomputed data must be provided").
- `make run/rentals-api` — run the API (plain `go run ./cmd/rentals-api`, no
  hot reload); `make run/api` is a deprecated alias
- `make build/rentals-api` — static binary (`CGO_ENABLED=0`) to
  `./bin/rentals-api` with the version embedded (`-X main.releaseVersion` /
  `main.buildTime`); reported by `/v1/healthcheck` and `/debug/vars`
  together with the VCS revision (`internal/shared/vcs`); `make build/api`
  is a deprecated alias
- `make test` — `go test -race ./...` (needs cgo; on this Windows machine it
  has no C compiler, so it is NOT the race gate)
- `make test/race` — **the race gate: run it for every gate/sub-step.** It runs
  `go test -race ./...` inside the golang Docker image (Docker must be up), with
  no `backend/.env` visible. Do not substitute `make test RACE=`.
- **Frontend gate** (run from `frontend/`): `pnpm -r lint && pnpm -r typecheck && pnpm -r test`
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
- **Read-only git is allowed**: `status`, `diff`, `log`, `show`,
  `check-ignore`, `describe`, `ls-files`.
- **Anything that changes repo state is forbidden**: `add`, `commit`,
  `push`, `checkout`, `restore`, `reset`, `stash`, `merge`, `rebase`, `rm`,
  `mv`. The user owns all git state and does the staging and committing.
  This is a permanent rule, not a per-task instruction — it applies to every
  future session in this repo, no exceptions, even if a task's wording
  implies otherwise. If a file needs to go back, ask the user.

## Phase 4b decisions (local staging)

- Local staging serves apps on `*.willcoll.localhost`, with Caddy using
  `tls internal` for certificates.
- `TRUSTED_PROXIES` governs client-IP trust (rate limiting, `audit_log` IP).
- Owner DB roles are BYPASSRLS and are used only by migrate/seed. APIs
  connect as the `_app` roles (subject to RLS), never as an owner.

## Authorization (docs/security/authorization.md, D1-D5 approved)

- Every route is registered through `guard(...)` (a catalog permission in
  `internal/shared/auth/permissions.go`) or `public(...)` (a reviewed entry in
  `public_routes.go`); `route_guards_test.go` fails the build otherwise.
- `/debug/vars` is not on any public router: it is served by the internal
  `METRICS_ADDR` listener (`httpx.NewMetricsServer`, off when empty). The
  production Caddyfile (4c) must also block `/debug/*`.
- Scopes: product (`module_access`), property (`staff.property_access`, enforced
  by RESTRICTIVE RLS `property_scope` policies fed from `app.property_ids`,
  rentals migration 000047), firm (`tenant_isolation`). Out of scope = 404. A new
  tenant table needs a `property_scope` policy or a reasoned exemption
  (`TestPropertyScope_EveryTenantTableHasAPolicyOrAnExemption`).
- Staff role / module_access / property_access / disable changes revoke the
  member's sessions (`authorityChange` in `cmd/platform-api/staff.go`).
- Step-up (C3, D5): `POST /v1/sessions/step-up` opens a 5-minute password
  re-entry window in the shared session (`internal/shared/auth/stepup.go`);
  `requireStepUp` gates channel changes, reversals and staff authority changes
  (403 `step_up_required`, the frontend `useApi` prompts and retries once via
  `withStepUp` in `layers/base`; a cancelled prompt leaves the action undone
  with a clear message). Conventions: put `requireStepUp()` AFTER the
  permission guard (a caller who may never do it gets a plain 403, not a
  prompt); every new permission that moves money or changes authority goes in
  `auth.StepUpPermissions` (a test fails on a `*.reverse` permission that is
  missing, and each API's route test fails on an ungated route); a new sign-in
  path must go through `loginPrincipal`, which clears any inherited window.
  Only the firm login grants or revokes the owner role (D3); `module_access`
  must name products the firm holds (M8). Details:
  `docs/security/authorization.md` "D5 as built" and section 4; the C3 brief is
  saved in `docs/security/c3-spec.md`.
- Owner alerts go through the DB-backed outbox, never straight to the mailer
  (`willcoll_platform.email_outbox`, `platformdata.OutboxModel`): call
  `app.alertOwner(...)` AFTER the action committed; it queues a row (idempotent
  per firm + event key, so give a natural key such as the reversed entry id
  when there is one) and platform-api's worker (`outbox_worker.go`) delivers
  with backoff, so a mail outage or restart can't fail or lose it. Failures
  show on the System page. Alerts carry no secrets and no raw phone numbers
  (`mailer.MaskPhone`). Product APIs only enqueue; only platform-api sends.
- Background jobs audit as `actor_type=system` with the job name in
  `audit_log.actor_name` (`app.auditSystem(job, tenantID, ...)`), and touch a
  firm's data only inside that firm's own tenant transaction.
- Product-to-platform HTTP calls use the service credential
  (`/internal/v1/*`, `SERVICE_TOKEN` / `SERVICE_TOKEN_PREVIOUS`,
  `auth.ServiceCredentials`): never a session, never in a URL or log, never
  routed by Caddy (the production Caddyfile, Phase 4c, must block `/internal/*`).
- Webhooks: the product and firm of a payment come from the `payment_channels`
  claim (`InboxModel.ClaimChannel`), never the payload; an unclaimed channel is
  not posted.
- Denied requests (401/403/scope-404) are logged by `httpx.DenialLog`
  (rate limited per actor, application log only); call `app.logDenied(c, perm)`
  before a hand-written 403 and `httpx.MarkScopeDenied(c)` before a scope 404.

## Money-channel rule

A PayHero channel with recorded receipts is never released or reassigned
(system-design A.6). The property may change or clear its channel setting,
but the claim in `payment_channels` stays with the original property.

## Rate limiters are in-memory, per instance

Every limiter and tracker in the APIs lives in process memory: the `httpx`
token-bucket limiters (per-IP; per-intent on pay status polling), the pay-OTP
per-IP window (`payotplimit.go`), and the health-page trackers. That is fine on
a single VPS. **Before running more than one instance of an API, move them to
Postgres or Redis** — with N instances every cap is effectively N times looser
and the trackers undercount. (The per-phone and per-firm OTP ceilings are
counted from `pay_otps` in Postgres, so those already hold across instances.)
Pay limits are deliberately loose per IP, because Safaricom carrier-grade NAT
puts many tenants behind one public IP: the real controls are per phone/unit
(3 codes / 10 min), per firm (`PAY_FIRM_DAILY_OTPS`, default 500/day, alerts on
the System page) and per intent (status polls, 1 req/s burst 3).

## Housekeeping notes

- `backend/.env` was removed from git tracking (kept on disk, now
  git-ignored) — it held local secrets and should never be committed.
  Real config templates live in `backend/configs/config.env.example`.
