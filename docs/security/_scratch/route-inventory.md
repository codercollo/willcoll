# Route & guard inventory (Phase B1 scratch)

Input for `docs/security/authorization.md` (Phase B2). Read-only code sweep;
nothing was executed. Items marked **VERIFY** were flagged by the sweep but
not confirmed from handler bodies. Resolve them in B2 before relying on them.

Legend: roles rank viewer < officer < accountant < owner. M = firm login
(implicit owner). SA = Super Admin. Audit: A = audited, E = exempt, - = read.
Default when a route is not in `routeMinRole`: GET/HEAD = viewer, other = owner.

## Global chains

- Both APIs, every route: recoverPanic, logRequest, metrics, authenticate
  (never rejects; resolves manager | staff | admin | anonymous), enableCORS.
  platform-api also runs loadAndSaveSession and touchSession.
- platform `guard` = requireActivatedManager (manager/staff of an `active`
  firm; admin gets 403). Owner-only routes add requireOwner.
- rentals manager group: requireActivatedManager, requireStaffModuleAccess
  ("rentals"), requireModule("rentals"), requirePlatformSubscription
  ("rentals") [no-op if subscriptionGate off], requireRole.
- requireRole: manager login always passes; staff need RoleAtLeast(role,
  minRole); unknown role fails closed. Only handler-level role check:
  `requireOwnerForChannelChange` (POST/PATCH /properties).
- No handler does its own ownership/property checks; scoping is RLS only.
  There is no property-level scope today.

## platform-api routes

| Method | Path | Gate | Audit | Who |
|---|---|---|---|---|
| GET | /debug/vars | none (Caddy only) | - | anyone reaching app port |
| GET | /v1/healthcheck | none | - | public |
| POST | /v1/managers | rate limit | E | public signup |
| PUT | /v1/managers/activated | token, NO rate limit | E | public |
| POST | /v1/tokens/activation | rl | E | public |
| PUT | /v1/staff/activated | invite token, NO rate limit | E | public |
| POST | /v1/sessions | rl | E | M + staff login |
| GET | /v1/sessions | none (401 if anon) | - | M, staff, SA |
| DELETE | /v1/sessions | none | E | any principal |
| POST | /v1/admin/sessions | rl | E | SA login |
| POST | /v1/tokens/password-reset | rl | E | public |
| PUT | /v1/managers/password | rl, reset token | E | public |
| GET | /v1/account | guard | - | M, all staff |
| PATCH | /v1/account | guard + owner | A | M, owner staff |
| PUT | /v1/account/password | guard + rl, NOT owner-gated | A | M and (VERIFY) any staff |
| PUT | /v1/staff/me/password | guard + rl, staff only | A | any staff role |
| GET | /v1/modules | guard (filtered by module_access) | - | M, staff |
| POST | /v1/staff | guard + owner | A | M, owner staff |
| GET | /v1/staff | guard + owner | - | M, owner staff |
| PATCH | /v1/staff/:id | guard + owner (status active/disabled; disable revokes sessions) | A | M, owner staff |
| POST | /v1/modules/:product/trial | guard + owner | A | M, owner staff |
| GET | /v1/billing/:product/plans | guard + owner | - | M, owner staff |
| GET | /v1/billing/:product/subscription | guard + owner | - | M, owner staff |
| POST | /v1/billing/:product/subscription/renew | guard + owner (STK push) | A | M, owner staff |
| GET | /v1/billing/:product/invoices | guard + owner | - | M, owner staff |
| GET | /v1/billing/:product/invoices/:id/pdf | guard + owner | - | M, owner staff |
| POST | /v1/webhooks/payhero/subscriptions | VerifyWebhook, no rl | E (logged) | PayHero |
| GET | /v1/admin/managers, /:id | requireAdmin | - | SA |
| POST | /v1/admin/managers/:id/suspend | requireAdmin (revokes all firm+staff sessions) | A (admin) | SA |
| POST | /v1/admin/managers/:id/reinstate | requireAdmin | A | SA |
| GET | /v1/admin/subscriptions | requireAdmin | - | SA |
| GET | /v1/admin/system/health | requireAdmin (forwards caller's Cookie to rentals) | - | SA |
| GET | /v1/admin/plans | requireAdmin | - | SA |
| POST | /v1/admin/plans | requireAdmin | E | SA |
| PATCH | /v1/admin/plans/:id | requireAdmin | E | SA |
| POST | /v1/admin/plans/:id/archive, /restore | requireAdmin | E | SA |

DB facts: `staff.role` CHECK in (owner, accountant, officer, viewer);
`staff.module_access text[] NOT NULL DEFAULT '{}'` with no CHECK (validated
only against rentals/estates/waste in `ValidateStaff`, not against the firm's
modules); `staff.status` in (invited, active, disabled), PATCH accepts only
active/disabled; RLS on staff. No property_access column exists.

## rentals-api routes

All behind the manager chain unless noted. Min role from `routeMinRole`;
"V" = default viewer (GET), "O" = default owner (write).

### Misc / public
| Method | Path | Gate | Audit |
|---|---|---|---|
| GET | /debug/vars | none (Caddy only) | - |
| GET | /q/:token | rl; QR token -> 302 | - |
| GET | /v1/healthcheck | none | - |
| POST | /v1/webhooks/payhero/collections | VerifyWebhook, no rl | E |
| GET | /v1/admin/system/health, /v1/admin/backups | requireAdmin | - |

### Public pay surface (rl on all)
| Method | Path | Protection |
|---|---|---|
| GET | /v1/pay/:slug/:unit | rl only, public unit info |
| POST | .../otp | phone must be on active lease; same 202 always; HMAC-hashed OTP, 5m TTL |
| POST | .../otp/verify | issues stateless HMAC pay-session token (tenant, unit, phone, 15m) |
| GET | .../balances | Bearer token, bound to URL unit+tenant |
| POST | .../intent | Bearer token, same binding |
| GET | /v1/pay/intents/:id/status | Bearer token; VERIFY intent belongs to token's unit |

### Landlords / properties / onboarding
| Method | Path | Min role | Audit |
|---|---|---|---|
| GET | /landlords, /landlords/:id | V | - |
| POST | /landlords | accountant | A |
| PATCH | /landlords/:id | accountant | A |
| GET | /properties, /properties/:id | V | - |
| POST | /properties | accountant (+owner if channel set) | A |
| PATCH | /properties/:id | accountant (+owner if channel changes) | A |
| DELETE | /properties/:id | owner | A |
| PATCH | /properties/:id/garbage, /electricity | accountant | A |
| GET | /properties/:id/electricity/deposits | V (JSON) | - |
| GET | .../electricity/deposits.csv, .pdf | accountant | - |
| PUT | /properties/:id/print-theme | accountant | A |
| GET | /onboarding | V | - |
| POST | /properties/:id/onboarding/import | accountant | A |

### Payment channels
| Method | Path | Min role | Audit |
|---|---|---|---|
| POST | /properties/:id/payment-channel | owner | A |
| GET | .../payment-channel/status | V | - |
| POST | .../payment-channel/test | owner (sends real prompt) | E |
| GET | /payment-channel-banks | V | - |

### Units / leases / payers / QR
| Method | Path | Min role | Audit |
|---|---|---|---|
| GET | /properties/:id/units | V | - |
| POST | /properties/:id/units | officer | A |
| POST | /properties/:id/units/import | accountant | A |
| GET | /units/:id | V | - |
| PATCH | /units/:id | officer | A |
| POST | /units/:id/leases | accountant | A |
| GET | /units/:id/leases, /leases/:id | V (tenant PII) | - |
| PATCH | /leases/:id | accountant | A |
| POST | /leases/:id/payers | officer | A |
| DELETE | /leases/:id/payers/:payerId | accountant | A |
| GET | /leases/:id/qr, /qr.png | V (live pay link) | - |
| POST | /leases/:id/qr/rotate | accountant | A |
| POST | /properties/:id/qr/bulk | accountant | A |

### Rent / ledger / payments
| Method | Path | Min role | Audit |
|---|---|---|---|
| PUT | /properties/:id/rent-schedule | accountant | A |
| POST | /properties/:id/rent/generate | accountant | A |
| GET | /properties/:id/rent/overview | V | - |
| POST | /units/:id/rent/payments | officer (cash receipt) | A |
| POST | /units/:id/rent/charges | accountant | A |
| POST | /ledger-entries/:id/reverse | accountant | A |
| GET | /units/:id/ledger/:type | V | - |
| POST | /units/:id/arrears-commitments | accountant | A |
| GET | /payments/review | V | - |
| POST | /payments/:id/allocate, /confirm | accountant | A |

### Water / garbage
| Method | Path | Min role | Audit |
|---|---|---|---|
| GET | /properties/:id/water | V | - |
| PUT | /properties/:id/water | officer | A |
| POST | /properties/:id/water/generate | accountant | A |
| GET | .../water/invoices/:period | accountant (PDF) | - |
| GET | /properties/:id/garbage/generate | V (preview) | - |
| POST | /properties/:id/garbage/generate | accountant | A |
| GET | .../garbage/invoices/:period | accountant (PDF) | - |

### Reports / receipts
| Method | Path | Min role | Audit |
|---|---|---|---|
| GET | /properties/:id/reports/:period | accountant | - |
| GET/PUT | .../reports/:period/plot-meter | V / officer | A (PUT) |
| GET | .../reports/:period/checks | accountant | - |
| POST | .../reports/:period/confirm | accountant | A |
| POST | .../reports/:period/generate | accountant | E (PDF) |
| GET | /properties/:id/receipts/:period/download | accountant | - |

There is no SMS-send, tenant-export or audit-log read route in rentals-api
today (SMS is sent only via the OTP route). `sms.send`, `exports.create` and
`audit.read` are catalog entries for future routes.

## System actors

| Actor | Auth mechanism |
|---|---|
| PayHero webhooks (both APIs) | `payhero.VerifyWebhook`: IP allowlist (ClientIP, TRUSTED_PROXIES) and/or secret (`X-Webhook-Token`, `?token=`, or HMAC `X-Payhero-Signature`); all configured checks must pass; fail-closed if none configured; 200 then background processing; not rate-limited. Token in query string can leak into logs. |
| Dedup | `payment_inbox` claim in platform before posting; local `mpesa_receipt` uniqueness |
| rentals -> platform | No HTTP, no service credential: two direct Postgres connections, `PLATFORM_DB_DSN` (app role, RLS) and `PLATFORM_DB_ADMIN_DSN` (BYPASSRLS). Used for usage upsert, module/subscription/session reads, inbox sweep. |
| Session verification in rentals | Reads the platform session cookie read-only (never mints/renews/destroys) |
| platform -> rentals | Admin health call forwards the SA's own Cookie; no service token |
| rentals jobs | usage recount (24h, all tenants), inbox sweeper (1m), `app.background` goroutines |
| platform jobs | none (only `app.background` for email/audit/webhook) |
| Health | `/v1/healthcheck` public on both; `/v1/admin/system/health` admin-only |
| Tenant pay session | Stateless HMAC token {TenantID, UnitID, Phone, Expires}, 15m; `paySessionFor` requires token unit+tenant == URL target |

## Frontend guards

- `layers/base/app/middleware/auth.global.ts` -> `guardRedirect` (pure,
  tested): anonymous -> login; non-admin on /admin -> home; expired access ->
  paywall-allowed paths only; admin outside /admin -> admin home; `return_to`
  via `safeReturnTo`.
- Principal model is only manager | admin. Staff fold into "manager";
  `isOwner` is true for the firm login or a staff owner.
- Platform: `owner.global.ts` redirects non-owners from /staff, /modules,
  /billing; nav hides Modules/Staff/Billing for non-owners; home cards
  filtered by module_access.
- Rentals: NO per-role checks on any page or button except the Billing nav
  link. Accountant/officer/viewer are never distinguished in the UI;
  disallowed actions surface as a 403 toast.
- `definePageMeta` carries only title/layout. No permission meta exists.

## Mismatches and gaps (raw, for B2 triage)

Backend gates
1. Tenant PII and money data readable at viewer: GET /units/:id/leases,
   /leases/:id, /payments/review, /units/:id/ledger/:type, rent/overview,
   water, electricity/deposits (JSON), onboarding, payment-channel/status.
   The CSV/PDF equivalents are accountant-gated, so the JSON versions bypass
   that intent. (Response bodies not read: confirm what each returns.)
2. GET /leases/:id/qr and qr.png are viewer: a viewer can print a working
   pay sticker while rotate/bulk are accountant.
3. GET /garbage/generate (preview) is viewer while its POST and PDFs are
   accountant.
4. POST /leases/:id/payers (authorizes a phone to pay) is officer while
   lease PATCH and payer DELETE are accountant.
5. Reversal is accountant with no second control; same tier as ordinary
   entry. High-risk.
6. /payment-channel/test is audit-exempt but sends a real prompt.
7. PUT /v1/account/password has no requireOwner and uses the manager row;
   **VERIFY** whether a staff session can change the firm password.
8. module_access not tied to the firm's actual modules (no CHECK, validator
   only checks the product name). platform-api has no module-based gate.
9. Unlisted routes silently inherit default tiers; nothing fails the build
   when a route is unlisted. (routeMinRole is the only table.)
10. No property-level scope exists anywhere.
11. Super Admin: requireAdmin routes only, and guard rejects SA on manager
    routes. Confirm SA also cannot reach rentals manager routes (authenticate
    resolves SA from the platform session in rentals).

System / public
12. /debug/vars has no in-app gate on either API (Caddy only).
13. PUT /managers/activated and PUT /staff/activated have no rate limit.
14. Webhook secret may travel in `?token=` (log leakage); IP-only config
    depends on correct TRUSTED_PROXIES; webhooks not rate-limited.
15. No per-code OTP attempt cap seen (only per-IP rate limit); 6-digit code
    guessable across IPs within the 5m TTL. **VERIFY.**
16. Intent status route does not take slug/unit; **VERIFY** the lookup is
    scoped to the token's unit/tenant so one tenant cannot read another's
    intent by ID.
17. No service credential between products and platform (shared DB roles);
    platform->rentals health relies on forwarding an SA cookie.

Audit
18. Reads are never audited (PII, PDFs, staff list, admin manager detail).
19. Admin plan writes are exempt from audit (global catalog, no tenant).
20. The coverage tests verify a call exists in the handler source, not that
    it runs on every path.

Frontend
21. Rentals UI shows all write controls to every role.
22. Client principal conflates staff with manager; owner handling exists
    only in platform.
