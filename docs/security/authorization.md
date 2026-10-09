# Authorization model (Phase 4-auth-1 — FINAL, D1-D6 approved; route matrix APPROVED 2026-10-09; C1 permissions, C2 scopes, C3 step-up/alerts and C4 matrix built)

Status: analysis only; no enforcement code exists for anything marked
"proposed". Source: `docs/security/_scratch/route-inventory.md` (route sweep)
plus handler spot-checks. Items under "Decisions needed" must be settled
before 4-auth-2 starts.

## 1. Actors

| Actor | Needs to do | Authenticates by | Data scope |
|---|---|---|---|
| Super Admin (Willcoll operator) | Oversee firms, suspend/reinstate, plan catalog, system health | Platform session, `admins` principal, separate login | Platform-wide metadata only. Never a firm's ledger via firm routes (`requireActivatedManager` rejects admin; to be pinned by test) |
| Firm Owner (manager login) | Everything in the firm | Platform session, email+password | Own firm (RLS `tenant_id`), all products it holds, all properties |
| Staff: owner | Same as firm login except firm-login-only actions (firm password) | Platform session (staff row) | Own firm; products in `module_access`; properties per `property_access` (proposed) |
| Staff: accountant | Money operations | same | same |
| Staff: officer | Day-to-day operations | same | same |
| Staff: viewer | Read-only | same | same |
| Tenant (unauthenticated) | Pay rent | QR/pay link + SMS OTP (phone must be on active lease, 5 attempts/code, 5m) -> stateless HMAC pay-session token (tenant, unit, phone, 15m) | One lease/unit only; `paySessionFor` binds token unit+tenant to the URL target; intent status is queried by (tenant, unit, intent id) |
| Landlord (future, Phase 5) | View own properties' statements | None today. Record as future actor: separate principal, scope = own landlord_id properties, read-only | n/a today |
| PayHero webhooks | Post payments / subscription settlements | `VerifyWebhook`: IP allowlist and/or secret/HMAC, fail-closed if none configured | Resolves tenant from channel claim; idempotent via `payment_inbox` + `mpesa_receipt` |
| Background jobs (usage recount, inbox sweeper, `app.background`) | Maintenance | In-process; direct DB roles | Cross-tenant by design (admin pool) |
| rentals -> platform service calls | Session/module/subscription reads, usage upsert, inbox claim | No HTTP credential: direct Postgres (`PLATFORM_DB_DSN` app role RLS; `PLATFORM_DB_ADMIN_DSN` BYPASSRLS) | Limited by DB grants |
| platform -> rentals | Admin health fan-out | Forwards the Super Admin's Cookie | Admin health only |
| Health checks | Liveness | None (`/v1/healthcheck`) | No data |

## 2. Permission catalog

Form `resource.action`. "Route(s)" uses shortened paths (`/v1` omitted).
Tiers show the **proposed** minimum role (see matrix; `*` = changed from
today, see section 5).

### rentals-api

| Permission | Route(s) | Min role |
|---|---|---|
| landlords.read | GET /landlords, /landlords/:id | viewer |
| landlords.write | POST /landlords, PATCH /landlords/:id | accountant |
| properties.read | GET /properties, /properties/:id | viewer |
| properties.write | POST /properties, PATCH /properties/:id, PATCH .../garbage, .../electricity, PUT .../print-theme | accountant |
| properties.delete | DELETE /properties/:id | owner |
| onboarding.read | GET /onboarding | viewer |
| onboarding.import | POST /properties/:id/onboarding/import | accountant |
| payment_channels.read | GET .../payment-channel/status, GET /payment-channel-banks | viewer |
| payment_channels.manage | POST .../payment-channel, POST .../payment-channel/test, and any channel field set on POST/PATCH /properties | owner |
| units.read | GET /properties/:id/units, /units/:id | viewer |
| units.write | POST /properties/:id/units, PATCH /units/:id | officer |
| units.import | POST /properties/:id/units/import | accountant |
| tenants.read | GET /units/:id/leases, /leases/:id | viewer |
| tenants.write | POST /leases/:id/payers | officer |
| leases.write | POST /units/:id/leases, PATCH /leases/:id, DELETE /leases/:id/payers/:id | accountant (decision D1) |
| qr.read | GET /leases/:id/qr, /qr.png | officer * |
| qr.manage | POST /leases/:id/qr/rotate, POST /properties/:id/qr/bulk | accountant |
| rent.read | GET .../rent/overview | viewer |
| ledger.read | GET /units/:id/ledger/:type | viewer |
| rent_schedule.write | PUT .../rent-schedule | accountant |
| charges.generate | POST .../rent/generate, .../water/generate, .../garbage/generate | accountant |
| charges.preview | GET .../garbage/generate | officer * |
| charges.write | POST /units/:id/rent/charges | accountant |
| charges.reverse | POST /ledger-entries/:id/reverse | accountant |
| arrears.write | POST /units/:id/arrears-commitments | accountant |
| payments.read | GET /payments/review | officer * |
| payments.record | POST /units/:id/rent/payments | officer |
| payments.allocate | POST /payments/:id/allocate, /confirm | accountant |
| water.read | GET .../water | viewer |
| water.write | PUT .../water | officer |
| invoices.read | GET .../water/invoices/:period, .../garbage/invoices/:period | accountant |
| plot_meter.read | GET .../reports/:period/plot-meter | viewer |
| plot_meter.write | PUT .../reports/:period/plot-meter | officer |
| reports.read | GET .../reports/:period, .../checks (on-screen only; **viewer**, D6) | viewer |
| reports.confirm | POST .../reports/:period/confirm, /generate | accountant |
| receipts.read | GET .../receipts/:period/download | accountant |
| exports.create | GET .../electricity/deposits.csv, .pdf and every future bulk tenant/ledger export | accountant |
| deposits.read | GET .../electricity/deposits (JSON) | officer * |
| sms.send | reserved: no route today (OTP is a system action) | accountant |
| audit.read | reserved: no route today (Phase 5k) | owner |

### platform-api

| Permission | Route(s) | Min role |
|---|---|---|
| session.self | GET/DELETE /sessions | any authenticated |
| account.read | GET /account | viewer |
| account.manage | PATCH /account | owner |
| account.password | PUT /account/password | firm login only (not a staff role) |
| staff.self_password | PUT /staff/me/password | any staff |
| modules.read | GET /modules | viewer (filtered by module_access) |
| modules.manage | POST /modules/:product/trial | owner |
| staff.read | GET /staff | owner |
| staff.manage | POST /staff, PATCH /staff/:id | owner |
| billing.read | GET /billing/:product/{plans,subscription,invoices,invoices/:id/pdf} | owner |
| billing.manage | POST /billing/:product/subscription/renew | owner |
| admin.managers.read | GET /admin/managers[/:id] | Super Admin |
| admin.managers.suspend | POST /admin/managers/:id/{suspend,reinstate} | Super Admin |
| admin.subscriptions.read | GET /admin/subscriptions | Super Admin |
| admin.plans.read / admin.plans.manage | GET / POST,PATCH /admin/plans... | Super Admin |
| admin.system.read | GET /admin/system/health (platform), /admin/system/health + /admin/backups (rentals), GET /admin/unclaimed-receipts (platform) | Super Admin |
| admin.receipts.resolve | POST /admin/unclaimed-receipts/:id/resolve (platform): close money received on an unclaimed channel, note required; audited under the claiming firm when the channel has since been claimed | Super Admin |

### Public allowlist (no permission; each with its own protection)

`POST /managers`, `PUT /managers/activated`, `POST /tokens/activation`,
`PUT /staff/activated`, `POST /sessions`, `POST /admin/sessions`,
`POST /tokens/password-reset`, `PUT /managers/password`,
`GET /healthcheck`, `GET /q/:token`, `/pay/*` (pay-session token),
`POST /webhooks/payhero/*` (signature). `/debug/vars` is NOT public: it is on the internal metrics listener only (M13).

## 3. Role -> permission matrix

Roles are cumulative (viewer ⊂ officer ⊂ accountant ⊂ owner) **except**
the owner-only block, which accountants never receive. "Firm login" = owner
plus `account.password`. Super Admin has `admin.*` only.

| Permission group | Viewer | Officer | Accountant | Owner | Super Admin |
|---|---|---|---|---|---|
| *.read (landlords, properties, onboarding, units, tenants, rent, ledger, water, plot_meter, account, modules, payment_channels), **reports.read (D6)** | yes | yes | yes | yes | no |
| qr.read, payments.read, charges.preview, deposits.read | no | yes | yes | yes | no |
| units.write, tenants.write, payments.record, water.write, plot_meter.write | no | yes | yes | yes | no |
| landlords.write, properties.write, onboarding.import, units.import, leases.write, qr.manage, rent_schedule.write, charges.generate/write, **charges.reverse**, arrears.write, payments.allocate, invoices.read, reports.confirm, receipts.read, **exports.create**, sms.send | no | no | yes | yes | no |
| properties.delete, **payment_channels.manage**, account.manage, modules.manage, staff.read/manage, billing.read/manage, audit.read | no | no | no | yes | no |
| account.password | no | no | no | firm login only | no |
| admin.* | no | no | no | no | yes |
| session.self, staff.self_password | all roles (staff.self_password: staff only) | | | | session.self |

Viewer explicit denials: exports.create, any bulk tenant phone/ID export,
sms.send, qr.read, payments.read, charges.preview, deposits.read, and every
report action except reading it on screen (reports.confirm / generate,
receipts.read, invoices.read, every .csv / .pdf: see D6). Officer explicit denials:
charges.reverse, payment_channels.manage, staff.*, billing.*, leases.write
(see D1). Accountant explicit denials: payment_channels.manage,
staff.manage, billing.manage, account.manage. Super Admin explicitly
cannot call any firm route (rentals chain rejects admin).

### Scopes (all applied together with the permission)

1. **Firm scope** — `tenant_id` + RLS, always. Unchanged.
2. **Product scope** — `staff.module_access`, always. Fix: validate against
   the firm's actual modules on write (M8).
3. **Property scope (proposed)** — new `staff.property_access` (platform DB).
   Properties live in the rentals DB, so there is no cross-DB FK:
   store `jsonb NOT NULL DEFAULT '{}'` keyed by product ->
   uuid list (`{"rentals":["<property id>",...]}`). Missing/empty = all
   properties, so every existing staff row keeps today's access. Owner and
   the firm login are never restricted. Enforcement belongs in the queries
   (a `property_id = ANY(...)` filter via one shared helper; optionally an
   RLS policy on `app.property_ids` as defense in depth — decide in C2).
   Out-of-scope resources return 404, not 403. A restricted staff member
   cannot create properties or see firm-wide aggregates (onboarding,
   property list is filtered). Archived/deleted property ids are pruned
   lazily.

### 3a. Property scope as built (C2)

- **Storage**: platform `staff.property_access jsonb` (migration platform
  000020), `{"rentals": ["<property id>", ...]}`. Missing or empty list = all
  properties. Owners and the firm login are never restricted (ignored when
  read, rejected when written). Validated: known product the member has
  `module_access` to, at most 200 ids, deduplicated.
- **Enforcement is in the database, not middleware.** `authenticate()` puts the
  list in the request context (`auth.WithPropertyScope`); every tenant
  transaction (`db.execTx`) sets `app.property_ids`; RESTRICTIVE `property_scope`
  RLS policies (rentals migration 000047) on every table that holds property
  data are ANDed with `tenant_isolation`. So lists, dashboards, reports,
  exports, search and joins are filtered by the queries themselves. Tables hang
  off their parent (leases -> units -> properties), so one rule governs each
  hop. `TestPropertyScope_EveryTenantTableHasAPolicyOrAnExemption` fails the
  build for a new tenant table that does not decide.
- **Out of scope = 404.** `requireResourceInScope` checks the path resource
  (property, unit, lease, landlord, payment, ledger entry) first for a scoped
  caller and answers 404 (it runs before the permission check, so a missing
  permission cannot be used to probe what exists). A write the database refuses
  maps to `auth.ErrOutOfScope`, answered 404 by `serverErrorResponse`.
- **Deliberate edges**: a scoped caller cannot create properties or landlords
  (403: the row would be invisible to them); unmatched payments (no unit) are
  invisible to a scoped caller, so only an unrestricted user places them;
  `transaction_headers` and `pay_otps` are not scoped (no tenant data / no staff
  principal). System work (webhooks, jobs, usage recount) has no principal and
  is unrestricted by design.
- **Revocation**: platform `PATCH /staff/:id` ends all of the member's sessions
  when role, module_access, property_access or status changes.

## 4. High-risk actions and their controls (as built in C3)

"Step-up" = the caller re-enters their own password; a window of 5 minutes
opens in the session (see "D5 as built"). "Alert" = an email to the firm login
through the DB-backed outbox (see "C3.7 as built").

| Action | Risk | Control as built | Still open |
|---|---|---|---|
| payment_channels.manage (register, test, and the channel field of a property create/update) | Redirects all rent to another till | Permission (owner), then step-up; alert on every change; a channel with receipts is never released | channel *test* is not alerted (no change) |
| charges.reverse | Erases money evidence | Permission (accountant), then step-up; mandatory reason; alert on every reversal (amount via moneyfmt, entry, reason, link to the unit) | second-person approval, deferred to Phase 8 (opt-in per firm) |
| staff.manage: invite, role / module_access / property_access change, removal (disable) | Privilege escalation | Permission (owner), then step-up (a rename or phone edit is not gated); sessions of the member ended; alert per kind of change; **owner role granted or revoked by the firm login only (D3)**, a staff owner gets a plain 403 before any prompt; `module_access` can only name products the firm holds (M8, 422); the firm login can never be locked out (it is not a staff row) | |
| payments.record (cash) | Fake receipts; arrears quietly cleared for a friend | **Pending until confirmed (C4.3):** a payment recorded by anyone who cannot confirm (no `payments.allocate`: the officer) is stored `pending` and posts NOTHING (no ledger entry, balance, arrears, report or receipt, no tenant SMS) until an accountant, the owner or the firm login confirms it; or rejects it with a reason (nothing posts, owner alerted). The recorder never confirms their own entry, whatever role they hold later. Both recorder and confirmer are in the audit trail. See "Payment confirmation as built" | daily total summary to the owner; second-person approval of an accountant's own cash entries (Phase 8) |
| Money fields on unit / lease / water routes (rent, deposits, service charge, rates, fees) | Quietly lowering what a tenant is charged | Accountant and above for any CHANGED amount (D1), through ONE shared guard; echoing the current value is allowed; a refusal writes nothing. See "Unit money-field guard as built" | |
| Super Admin: suspend / reinstate a firm, resolve an unclaimed receipt, create / edit / archive / restore a plan | The most valuable account in the system | Permission (admin.*), then **step-up with the ADMIN password** (5-minute window in the admin session); each action writes the Super Admin's own trail (`admin_audit_log`: actor, IP, target, reason or note). See "Super Admin step-up as built" | **Super Admin 2FA (TOTP) before go-live (Phase 9)** |
| Landlord payout details (bank account, M-Pesa number, paybill / till used for remittances) | Redirecting payouts is the most common fraud in property management | **Planned (Phase 5), not built:** any create or change is owner-only + step-up + owner alert (C3.7 outbox) showing old and new values masked; a newly changed payout destination is held 24 hours before the first remittance uses it (cooling-off) unless the owner explicitly confirms | everything: planned |
| account.password / PATCH account | Takeover | Current password required; other sessions ended; any step-up window ends with the session | notify email on change |
| properties.delete | Hides property data | Archive only; owner permission | step-up |
| billing.manage | Spend | Owner-only | none needed |
| onboarding.import / units.import | Bulk overwrite | Accountant, audited | row-count confirmation |
| qr.manage rotate/bulk | Invalidates printed stickers | Accountant | |
| exports.create / bulk tenant data | PII exfiltration | Accountant; viewer denied | audit reads of exports (M17) |
| admin.managers.suspend | Outage | Audited as actor_type=admin; all sessions of the firm ended; step-up and an optional reason (C4.3) | |

### Other actors (C3.1-C3.5 as built)

| Actor | Control |
|---|---|
| Tenant (pay link) | Token bound to the lease, lease re-checked on every pay route, another unit's URL is 404; OTP: 5 attempts per code, per-phone/unit/IP/firm ceilings; per-IP and per-intent request limits |
| PayHero webhook | Source-IP allowlist and/or secret (`X-Webhook-Token`, `?token=`, or HMAC-SHA256 body signature, all constant-time); bad or missing = 401, logged, nothing written. Replay: the M-Pesa receipt is the idempotency key (inbox claim, then the ledger's own receipt uniqueness); PayHero's callback carries no timestamp this code reads, so there is no tolerance window. The channel decides ownership: the payment's product and firm come from the `payment_channels` claim (admin-pool lookup), never the payload; a channel with no claim is **not posted** (alarm with the full payload logged + System page "unclaimed channels"); a channel claimed by another product is refused; a payload whose intent resolves to another firm than the claim is refused |
| Product API -> platform | `/internal/v1/*` on platform-api accepts only `Authorization: Bearer <SERVICE_TOKEN>` (constant-time, `SERVICE_TOKEN_PREVIOUS` accepted during rotation, never a session, never in a URL or log). Not routed by Caddy (local Caddyfile answers 404 for `/internal/*`; production Caddyfile is Phase 4c and must do the same). Endpoints: `GET /internal/v1/health`, `POST /internal/v1/usage`. Today rentals-api still writes `module_usage` through its database role (M16); the endpoint exists for the products that move to it |
| Background jobs | `actor_type=system` with the job name in `audit_log.actor_name` (migration platform 000021; `actor_id` is a uuid and a job has none): `usage-recount`, `inbox-sweeper`. Each write opens its own tenant transaction for the firm it names, so one firm's scope never carries to the next |
| Denied requests | 401, 403 and property-scope 404 are logged (application log only, never `audit_log`) with actor, permission, method, route template, IP and request id; rate limited per actor (10 lines/min, per IP when anonymous), with `suppressed_before` on the next window's first line. Every response carries `X-Request-Id` |

### C3.7 as built (owner alerts)

- Alerts are rows in `willcoll_platform.email_outbox` (migration 000022),
  queued by whichever API raised them; platform-api's worker (every 15 s)
  claims due rows with `FOR UPDATE SKIP LOCKED` and a 2-minute lease, sends,
  and settles. Failures retry at 30 s, 2 m, 10 m, 30 m, 1 h, 2 h, 4 h, then
  stay `failed`. A restart loses nothing; delivery is at least once.
  (Superseded by C4.0/C4.1 below: 10 attempts, then `dead`.)
- Idempotent per event: `UNIQUE (manager_id, event_key)`. A reversal is keyed by
  the entry, everything else by request id + kind + entity.
- A failed email never fails or rolls back the action (the alert is queued
  after it). Failures show on the Super Admin System page: waiting, failed, last
  error, and alerts that could not even be queued.
- Content: what happened, actor name and role (or "firm login"), IP, target,
  amount (moneyfmt), time in Africa/Nairobi, link to the record. No secrets and
  no phone numbers (`mailer.MaskPhone` exists for any future alert that needs one).
- Triggers: payment channel change; any reversal; staff role, module_access or
  property_access change; staff removal; staff invite.

### C4.0 as built (transactional outbox, dead alerts)

- **Platform actions** (staff role, module, property, removal; channel changes)
  enqueue with `EnqueueIn` through a `TxHook` inside the action's own
  transaction: the alert commits with the action or not at all.
- **Rentals actions** (charge reversals) cannot share a transaction with the
  platform database. The alert is written to `willcoll_rentals.alert_outbox`
  (migration 000049) in the reversal's transaction; a relay job in rentals-api
  (every 15 s, `alert_relay.go`) hands pending rows to the platform outbox and
  marks them `relayed`. The platform enqueue is idempotent per (firm, event key),
  so a crash between the two steps, a second instance or a retry sends one email.
- **Dead alerts**: after 10 failed attempts a row becomes `dead` (platform
  `email_outbox`, migration 000024, replaces `failed`; Rentals `alert_outbox`,
  migration 000050). It is never retried, is logged at error level, and is counted
  on the System page ("Owner alerts dead", "Rentals alerts dead"). The Rentals
  relay spaces its attempts with the platform backoff (`next_attempt_at`), so a
  short platform outage cannot use up the budget.
- Tests: rollback leaves no alert on either side; crash after commit before the
  relay still delivers; duplicate relay sends one email; dead after exactly 10
  attempts and never claimed again (unit and Postgres integration).

### C4.2 golden route x role matrix

`cmd/rentals-api/route_matrix_integration_test.go` and
`cmd/platform-api/route_matrix_integration_test.go` call EVERY registered route
as each of ten principals (anonymous, tenant pay token for unit A1, viewer,
officer, officer scoped to property A, accountant, staff owner, firm login,
Super Admin, service credential) through the real router and a real database,
with path parameters from a seeded fixture (property B's resources, except the
pay routes, which use unit A1). Anything that can change data runs on a
brand-new fixture, so one principal's call never alters what the next sees. The
observed status (with `h` when the route's own handler ran, and `step-up` for a
`step_up_required` refusal; see C4.3 below) is
compared with `internal/shared/auth/testdata/route_matrix.golden`;
`docs/security/route-matrix.md` is generated from it. Regenerate only with
`go test ./cmd/platform-api ./cmd/rentals-api -run RouteMatrix -update`, and read
the diff: every change in the golden file is a change in who can call what.

Asserted in code (`routematrix.CheckInvariants`), not only in the file:
anonymous, pay token and service credential never get 2xx on a guarded route;
viewer never 2xx on a write or on `exports.create`; the Super Admin never 2xx on
a firm route; the scoped officer always 404 on property B's resources; a pay
token never 2xx on unit B1's pay routes; the service credential enters
`/internal/v1/*` and nobody else does; step-up appears on exactly the declared
routes; and for every guarded route each principal's answer agrees with the
permission catalog (`StaffPermits`, `FirmLoginPermits`, `AdminPermits`).
Database-free drift tests also fail a plain `go test` when the golden file and
the registered routes differ, when a route has neither a permission nor a public
allowlist entry, when `stepUpGatedRoutes` and `auth.StepUpPermissions`
disagree, when `route-matrix.md` is stale, or when a catalog permission is
missing from this document.

The matrix records the access rule, not timing: the Rentals Super Admin health
and backup pages scan catalog statistics, which on a cold scratch database can
outlast the per-query timeout and answer 500 / 503, so the Rentals test sets
`Platform.HealthTimeout` to two minutes. (A first version recorded those cells
as 500 / 503 and flipped to 200 in a full run; the golden file must not depend
on what other tests leave in the database.)

### C4.3 as built (matrix review fixes: `docs/security/c4-matrix-fixes.md`)

**An empty body proves nothing about a rule that depends on WHAT is changed**, so
the matrix also holds *variant rows*, `METHOD /path [variant]`, each the same route
with a realistic body (`PATCH /v1/staff/:id [role change]`, `PATCH /v1/properties/:id
[channel change]`, `PATCH /v1/units/:id [rent change]`, ...). Each mutating variant
runs on a fresh fixture. `routesWithFieldChecks` (one list per API, in
`route_matrix_integration_test.go`) names every route with a field-level rule and
the variant row that covers each protected field; drift tests fail when a listed
row is missing from the golden file, or when a handler's source calls a field-check
(`requireOwnerForChannelChange`, `staffActorMayNotChangeOwner`, an in-handler
`stepUpSatisfied`, the money-field guard, the pending-payment rule) but is not listed.

**Cell codes.** `404h`/`422h`/`409h`/`400h`/`503h` = the handler ran (authorization
let the request through) and returned that code; plain `404` = refused by scope;
`403` = refused by permission; `401` = not authenticated; `step-up` = refused until
the password is re-entered; `step-up>200*` (shown `step-up → 2xx*`) = the same request
repeated with a fresh step-up window then succeeded. The "handler ran" mark is a
response header (`X-Test-Handler-Reached`) set by `auth.MarkReached` ONLY when an
API's unexported `markHandlers` field is true; only the matrix tests set it, no
wrapper is installed otherwise, and tests pin both (off by default; no non-test file
turns it on).

**Payment confirmation as built (item 3).** `POST /v1/units/:id/rent/payments`
(cash or bank) stores the payment `pending` when the caller lacks
`payments.allocate` (the officer): migration `000051_pending_manual_payments`
(`status` gains `pending`/`rejected`; `pending_ledger_type`, `recorded_by`,
`recorded_by_role`, `confirmed_by/at`, `rejected_by/at`, `reject_reason`). A pending
payment names its unit and ledger and posts NOTHING: no transaction header, ledger
entry, allocation, receipt, balance, arrears figure or report line, and no SMS (hand
entered payments never send one). It is in `GET /v1/payments/review` (with who
recorded it and in what role) and counts against "no unallocated payments" in the
period sanity check. `POST /v1/payments/:id/confirm` (accountant, owner, firm login)
posts it through the one posting path, exactly once, recording `confirmed_by/at`;
confirming again is a **409**; the person who recorded it is refused (**403**, "a
payment cannot be confirmed by the person who recorded it"), a promoted officer
included. `POST /v1/payments/:id/reject {"reason"}` (same permission) refuses it:
nothing posts, the row is kept as `rejected` with who, when and why, and an owner
alert is queued in the same transaction (event key `payment.rejected:<id>`); a
rejected payment's reference can be entered again. Accountant, owner and firm-login
payments post at once, as before. The review response carries `summary.pending` and
`summary.pending_over_48h`, shown on the dashboard. Audit trail: `payment.recorded_pending`
(actor and role), `payment.confirmed` (confirmer, recorder), `payment.rejected`.
Still open: second-person approval of an accountant's own cash entries is Phase 8.

**Unit money-field guard as built (item 4).** `PATCH /v1/units/:id` and `POST
/v1/properties/:id/units` accept only `unit_code` and `meter_number`: a unit has no
money of its own (rent and deposits live on the lease, rates and fees on the
property). The one real officer-reachable money field was the per-reading `rate` of
`PUT /v1/properties/:id/water`, which overrides the water rate for that bill. All of
these now run ONE guard (`cmd/rentals-api/moneyguard.go`, `refuseMoneyChange`,
capability `rent_schedule.write` = accountant and above): a request that carries a
money field (`rent_amount`, `rent_deposit_amount`, `water_deposit_amount`,
`electricity_deposit_amount`, `service_charge`, `water_rate`, `water_rate_per_unit`,
`garbage_fee`, or a reading's `rate`) with a CHANGED value gets **403** from anyone below
accountant and writes nothing (no partial apply); echoing the current value is allowed
(full-object forms keep working); an accountant sending a changed amount to a unit
route gets a 422 pointing at the lease. Unit creation by an officer is allowed
when the money fields are absent or zero and 403 otherwise (the workflow, adding units
during setup, is unchanged). Every other route that writes unit, lease or property
money (leases, rent schedule, garbage, electricity, imports) is already accountant
and above.

**Super Admin step-up as built (item 6).** `POST /v1/sessions/step-up` now works for a
Super Admin session: the ADMIN password is checked (never a firm's), the window lives
in the admin session and dies with it, five wrong entries end the session.
`admin.managers.suspend` (suspend, reinstate), `admin.receipts.resolve` and
`admin.plans.manage` (create, edit, archive, restore) are in `auth.StepUpPermissions`
and gated; the drift tests tie routes to that list. Each such action, and each admin
step-up, appends a row to `admin_audit_log` (migration `000026`): admin id, client IP,
target firm (when there is one), action, entity and the reason or note; suspend and
reinstate take an optional `{"reason"}`. The firm's own `audit_log` still gets the
firm-targeted ones. TOTP 2FA for the Super Admin is a Phase 9 item.

### C4.1 authorization backfill (`devtools authz-backfill`)

`go run ./cmd/devtools authz-backfill --dry-run | --apply` (any ENV; needs
`PLATFORM_DSN` and `RENTALS_ADMIN_DSN`). Always run the dry run first and read
the counts. It reports ids only, never names or phones.

**The rule: it may only narrow or keep access, never widen it.** "Access" is
defined once (`effectiveOf`, `cmd/devtools/authz_plan.go`) from the runtime's own
rules, and a test compares before and after for every row. Consequences that
differ from the first wording of the spec:

| Finding | Change | Why |
|---|---|---|
| role NULL or not one of the four | role -> viewer **and the account disabled** | an unknown role grants nothing today; viewer alone would grant something |
| module_access: duplicate or unknown | dropped | no effect on access |
| module_access: wrong case or spacing ("Rentals") | **dropped, not lowercased**; reported | access is an exact-string match, so lowercasing would grant a product the member does not have |
| module_access: product the firm no longer holds | **reported only**, kept | M8: existing users are not broken |
| property_access: ids deleted, archived or of another firm | dropped | narrows only |
| property_access: nothing live left, or malformed | stored as the nil uuid (no property) | an empty list means every property, so pruning to empty would widen; a malformed value is already closed at runtime |
| inactive staff with live sessions, sessions of deleted staff | sessions destroyed | |
| staff whose firm does not exist | disabled, sessions destroyed | no audit row is possible (no firm to attach it to) |
| active firm login that cannot sign in (no email or password) | **reported only** | expected 0 |

Owners are never restricted, so their property_access is left alone.

Apply writes the staff row (optimistic `version` lock) and one audit row per
change in one transaction: `actor_type=system`, `actor_name=authz-backfill`,
action `authz_backfill.<category>`, summary = the rule and counts. Session
revocation rows are written after the sessions are destroyed, with the number
really destroyed. It is idempotent: a second run reports 0. It is a devtool and
not a migration so that nothing changes on deploy before a person has read the
dry-run counts, and because the property check needs the Rentals database. The
admin role needed INSERT on `audit_log` for this (platform migration 000025).

## 5. Mismatches between matrix and today's behaviour

Resolved during B2 verification (not mismatches): `PUT /account/password`
already rejects a staff session (`account.go:96`); OTP has a 5-attempt cap per
code (`pay_access.go` `otpMaxAttempts`); intent status is queried by
(tenant, unit, intent id) from the token (`pay.go:324`) — add a cross-tenant
test in C3 anyway.

| # | Mismatch | Today | Target |
|---|---|---|---|
| M1 | Tenant-PII/money JSON reads at viewer | `payments/review`, electricity deposits JSON | payments.read officer, deposits.read officer |
| M2 | QR image/link readable by viewer | viewer | qr.read officer |
| M3 | Garbage preview viewer, run accountant | viewer | charges.preview officer |
| M4 | payers add officer vs lease edit accountant | split | keep, flag as D1 |
| M5 | Reversal same tier as entry, no extra control | **DONE (C3)** | accountant + step-up + owner alert (section 4) |
| M6 | channel test audit-exempt but sends a real prompt | exempt | audit it |
| M7 | Unlisted routes silently get default tier | `routeMinRole` fallthrough | deny by default; test fails if a route has no permission |
| M8 | `module_access` not validated against firm modules | **DONE (C3.8)** | 422 when invite names, or an update adds, a product the firm does not hold (trialing or active in `manager_modules`) |
| M9 | No property scope | **DONE (C2)** | `staff.property_access`, enforced by RLS (see section 3a) |
| M10 | Rentals UI never hides controls by role | all roles see all buttons | permission-driven UI (4-auth-3) |
| M11 | Client principal conflates staff and manager | manager | `GET /v1/me` returns permissions |
| M12 | Role names baked into 6 route entries + 2 handler checks | scattered | single catalog in `internal/shared/auth` |
| M13 | `/debug/vars` unauthenticated in-app on both APIs | **DONE (C2)** | removed from both public routers; served only by the internal `METRICS_ADDR` listener (`httpx.NewMetricsServer`); tests assert 404 on the public router; Caddy blocks `/debug/*` too |
| M14 | Activation endpoints (`PUT /managers/activated`, `PUT /staff/activated`) have no rate limit | none | add rate limit |
| M15 | Webhook secret may ride `?token=` (log leakage), webhooks unthrottled | accepted | prefer header/HMAC; document |
| M16 | No service credential between products and platform | **PARTLY DONE (C3.3)**: `/internal/v1/*` + `SERVICE_TOKEN` exist; products still write `module_usage` through their DB role | move each product's usage report onto the endpoint when the DB grant is tightened |
| M17 | Reads never audited (PII, PDFs, staff list, exports) | none | audit exports.create at least |
| M18 | Admin plan writes audit-exempt | exempt | optional: audit as actor_type=admin |
| M19 | Audit coverage tests only prove a call exists in source | source check | acceptable; keep |
| M20 | Staff role changes do not revoke sessions on demotion | **DONE (C2)** | role, module_access, property_access and disable all end the member's sessions (`authorityChange`); permissions are also re-read per request |
| M21 | Super Admin cannot reach firm routes — not pinned by test | by chain | route x role test (C4) |

Routes with no explicit gate (rely on default tier): every GET in the
rentals inventory not in `routeMinRole` (about 25) and every unlisted write
(owner). After 4-auth-2 none may remain.

## 6. Decisions needed before 4-auth-2

- **D1 Officer and move-ins.** The brief says officers handle tenant
  details and move-ins, but lease create/patch is accountant today because
  a lease carries rent/deposit terms. Options: (a) keep accountant (no
  behaviour change; officer only adds payers, recommended); (b) let officers
  create leases but split a `leases.terms` permission for rent/deposit edits
  (needs a route/body split).
- **D2 Property scope storage**: the jsonb-by-product layout above vs a
  `staff_property_access` table in the rentals DB. Recommended: jsonb in
  platform (one place permissions are read per request).
- **D3 Who may grant owner role**: firm login only (recommended) or any owner.
- **D4 The `*`-marked tier changes** (qr.read, payments.read, charges.preview,
  deposits.read) lower viewer access; confirm no real viewer workflow breaks.
- **D5 Extra controls** in section 4: **RESOLVED** (see below).

### D5 resolution (approved by the user)

Build in C3 (not later):

- **Step-up password re-entry, valid 5 minutes**, required for:
  `payment_channels.manage`, staff role/permission changes, staff removal,
  and reversals (`charges.reverse`; there is no separate `payments.reverse`
  route today, so any future payment-reversal permission inherits this).
- **Owner email alert** (Mailpit locally) on payment channel change, any
  reversal, and any staff role change. Each alert includes actor, IP,
  amount/target and time (Africa/Nairobi).

#### D5 as built (C3)

- **Window**: `POST /v1/sessions/step-up` (platform-api, the only binary that
  writes sessions, ADR-0007) takes `{password}`, checks it against the
  caller's OWN password (the firm login's, or the staff member's), and stamps
  `stepup_until` (now + 5 minutes) into the shared platform session. Rentals
  reads it read-only in `resolvePlatformPrincipal`. It is per session, dies
  with the session, and is shared across products. Primitives:
  `internal/shared/auth/stepup.go`.
- **Gated**: rentals `POST .../payment-channel`, `POST .../payment-channel/test`,
  `POST /ledger-entries/:id/reverse` (`requireStepUp`), and the channel field on
  `POST`/`PATCH /properties` (only when it actually changes, in
  `requireOwnerForChannelChange`); platform `POST /staff` (a grant) and
  `PATCH /staff/:id` when role, module_access, property_access or status change
  (a rename or phone edit is not gated). Staff removal is the disable status;
  there is no delete route. There is no separate payment-reversal route today.
- **Order**: the permission check runs first, so someone who may never do the
  action gets the plain 403, not a password prompt. A missing window is 403 with
  `code: "step_up_required"`; the client (`useApi`) opens one password box,
  calls step-up and retries once.
- **Brute force**: a wrong password is 422 (not 401, which the frontend treats
  as signed out), rate limited per IP, and the session is ended after 5 wrong
  re-entries in a row (`StepUpMaxFailures`). Success and failure are audited
  (`session.step_up`, `session.step_up_failed`).
- **Alerts**: `security_alert.tmpl` to the firm login's email (Mailpit locally)
  on a payment channel change (register, or the property field), every
  reversal (amount, entry id, reason), and a staff role change or invite. Each
  carries actor (name and role, or "firm login"), IP, target and the time in
  Africa/Nairobi. They are sent in the background: a mail outage is logged and
  never fails the action. Sent even when the owner is the actor.
- **Not alerted**: the channel *test* (no change), module/property scope edits.
  Say so if you want them added.

Deferred to Phase 8: second-person approval for reversals. Many firms are
one or two people and it would block them; when built it must be an opt-in
per-firm setting.

D1-D4 approved (see below). Matrix is final.

### D1-D4 resolution (approved by the user)

- **D1**: lease create/edit stays accountant; officers add payers only.
- **D2**: property scope stored in the platform DB as `staff.property_access`
  jsonb keyed by product; empty = all properties.
- **D3**: only the firm login can grant the owner role. **Enforced (C3.8):**
  a staff session inviting an owner, or changing a member's role to or from
  owner, gets a plain 403 (checked before the step-up prompt); tested in
  `cmd/platform-api/staff_c38_test.go`.
- **D4**: the four viewer reductions are accepted and move to **officer**:
  qr.read, payments.read, charges.preview, deposits.read.
- **D6 (viewer reads reports on screen; C4.3)**: a landlord or auditor given a
  viewer account expects to see the monthly report. `reports.read` is granted to
  **viewer** for the on-screen views only (`GET .../reports/:period` and
  `.../checks`). Generate and confirm (`reports.confirm`), receipt download
  (`receipts.read`), invoice PDFs (`invoices.read`) and every `.csv` / `.pdf` /
  export route (`exports.create`) stay accountant and above. The report payload
  carries property name and location, house numbers, tenant names, per-ledger
  payment dates and amounts, totals, NOTE:1 / NOTE:2 text and the management fee: all
  of it already readable by a viewer through the unit, lease and ledger routes. It
  carries no phone number, ID number or email, so there was nothing to mask; a test
  (`TestReportPayloadCarriesNoContactData`) fails if a contact-data field is ever added
  to it without masking it for the viewer first.

With D1-D5 approved, this matrix is final; changes need a new review.

### Route matrix approval (2026-10-09)

The golden route x role matrix (`internal/shared/auth/testdata/route_matrix.golden`,
rendered in `docs/security/route-matrix.md`) is **approved** by the user on
2026-10-09, including D6, conditional on:

1. **Landlord payout destination.** The payout fields of `POST /v1/landlords` and
   `PATCH /v1/landlords/:id` (today `bank_name`, `bank_account_name`,
   `bank_account_number`; the list is `payoutField` in `cmd/rentals-api/landlords.go`,
   so an M-Pesa number, paybill/till, branch or account reference is added there)
   follow the payment-channel rule through the shared field guard
   (`requireOwnerStepUp`, via `requireOwnerForPayoutChange`): changing one is
   owner-only (an accountant or below gets 403, nothing written), needs step-up, and
   queues an owner alert in the same transaction as the save (the account number
   shown as last four characters, old and new; the other fields as "changed"). Echoing
   the current value is not a change, and every other landlord field stays
   accountant. Variant rows: `POST /v1/landlords [with payout]`,
   `PATCH /v1/landlords/:id [payout change | payout echo | name only]`, listed in
   `routesWithFieldChecks`. The 24h cooling-off on a changed payout destination stays
   a Phase 5 item (remittances).
2. Housekeeping and tooling: no large files in the repo; a PreToolUse hook blocks
   in-place edits and state-changing git (see CLAUDE.md).
