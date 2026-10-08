# Authorization model (Phase 4-auth-1 — FINAL, D1-D5 approved)

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
| reports.read | GET .../reports/:period, .../checks | accountant |
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
| admin.system.read | GET /admin/system/health (platform), /admin/system/health + /admin/backups (rentals) | Super Admin |

### Public allowlist (no permission; each with its own protection)

`POST /managers`, `PUT /managers/activated`, `POST /tokens/activation`,
`PUT /staff/activated`, `POST /sessions`, `POST /admin/sessions`,
`POST /tokens/password-reset`, `PUT /managers/password`,
`GET /healthcheck`, `GET /q/:token`, `/pay/*` (pay-session token),
`POST /webhooks/payhero/*` (signature), `/debug/vars` (see mismatch M13).

## 3. Role -> permission matrix

Roles are cumulative (viewer ⊂ officer ⊂ accountant ⊂ owner) **except**
the owner-only block, which accountants never receive. "Firm login" = owner
plus `account.password`. Super Admin has `admin.*` only.

| Permission group | Viewer | Officer | Accountant | Owner | Super Admin |
|---|---|---|---|---|---|
| *.read (landlords, properties, onboarding, units, tenants, rent, ledger, water, plot_meter, account, modules, payment_channels) | yes | yes | yes | yes | no |
| qr.read, payments.read, charges.preview, deposits.read | no | yes | yes | yes | no |
| units.write, tenants.write, payments.record, water.write, plot_meter.write | no | yes | yes | yes | no |
| landlords.write, properties.write, onboarding.import, units.import, leases.write, qr.manage, rent_schedule.write, charges.generate/write, **charges.reverse**, arrears.write, payments.allocate, invoices.read, reports.read/confirm, receipts.read, **exports.create**, sms.send | no | no | yes | yes | no |
| properties.delete, **payment_channels.manage**, account.manage, modules.manage, staff.read/manage, billing.read/manage, audit.read | no | no | no | yes | no |
| account.password | no | no | no | firm login only | no |
| admin.* | no | no | no | no | yes |
| session.self, staff.self_password | all roles (staff.self_password: staff only) | | | | session.self |

Viewer explicit denials: exports.create, any bulk tenant phone/ID export,
sms.send, qr.read, payments.read, charges.preview, deposits.read. Officer explicit denials:
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

## 4. High-risk actions and recommended extra controls (recommend only)

| Action | Risk | Recommended control |
|---|---|---|
| payment_channels.manage | Redirects all rent to another till | Re-enter password; email the firm owner on every change; keep the existing "channel with receipts is never released" rule |
| charges.reverse / payments reversal | Erases money evidence | Mandatory reason (exists); optional second approval above a threshold; alert owner on reversals by non-owner |
| payments.record (cash) | Fake receipts | Officer allowed, but daily total summary to owner; audit with actor (exists) |
| staff.manage — role change, enable, **granting owner** | Privilege escalation | Re-enter password; only the firm login (not a staff owner) may grant or revoke owner; never leave the firm with zero active sign-in path; email on role change |
| account.password / PATCH account | Takeover | Current password (exists); notify email on change |
| properties.delete | Hides property data | Re-enter password; archive only (exists) |
| billing.manage | Spend | Owner-only (exists); no extra needed |
| onboarding.import / units.import | Bulk overwrite | Accountant, audited (exists); row-count confirmation |
| qr.manage rotate/bulk | Invalidates printed stickers | Accountant (exists) |
| exports.create / bulk tenant data | PII exfiltration | Audit reads of exports (none today, M18); rate limit |
| admin.managers.suspend | Outage | Audited (exists); consider reason field |

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
| M5 | Reversal same tier as entry, no extra control | accountant | accountant + section 4 controls |
| M6 | channel test audit-exempt but sends a real prompt | exempt | audit it |
| M7 | Unlisted routes silently get default tier | `routeMinRole` fallthrough | deny by default; test fails if a route has no permission |
| M8 | `module_access` not validated against firm modules | any product name | validate against held modules |
| M9 | No property scope | none | property_access (proposed) |
| M10 | Rentals UI never hides controls by role | all roles see all buttons | permission-driven UI (4-auth-3) |
| M11 | Client principal conflates staff and manager | manager | `GET /v1/me` returns permissions |
| M12 | Role names baked into 6 route entries + 2 handler checks | scattered | single catalog in `internal/shared/auth` |
| M13 | `/debug/vars` unauthenticated in-app on both APIs | Caddy only | keep behind Caddy; add to explicit allowlist + admin-only alternative |
| M14 | Activation endpoints (`PUT /managers/activated`, `PUT /staff/activated`) have no rate limit | none | add rate limit |
| M15 | Webhook secret may ride `?token=` (log leakage), webhooks unthrottled | accepted | prefer header/HMAC; document |
| M16 | No service credential between products and platform | shared DB roles | accept for now (same host/DB cluster); document; 4-auth-2 keeps signature checks and limits via DB grants |
| M17 | Reads never audited (PII, PDFs, staff list, exports) | none | audit exports.create at least |
| M18 | Admin plan writes audit-exempt | exempt | optional: audit as actor_type=admin |
| M19 | Audit coverage tests only prove a call exists in source | source check | acceptable; keep |
| M20 | Staff role changes do not revoke sessions on demotion | verify in C2: disable revokes, role change unconfirmed | permissions resolved per request; revoke on demote/remove |
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

Deferred to Phase 8: second-person approval for reversals. Many firms are
one or two people and it would block them; when built it must be an opt-in
per-firm setting.

D1-D4 approved (see below). Matrix is final.

### D1-D4 resolution (approved by the user)

- **D1**: lease create/edit stays accountant; officers add payers only.
- **D2**: property scope stored in the platform DB as `staff.property_access`
  jsonb keyed by product; empty = all properties.
- **D3**: only the firm login can grant the owner role.
- **D4**: the four viewer reductions are accepted and move to **officer**:
  qr.read, payments.read, charges.preview, deposits.read.

With D1-D5 approved, this matrix is final; changes need a new review.
