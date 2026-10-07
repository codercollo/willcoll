# 0007. A separate platform-api, and one session cookie across every host

Status: accepted

## Context

Once Estates and Waste exist, a manager needs ONE login that works across
`rentals.willcoll.co.ke`, `estates.willcoll.co.ke` and `waste.willcoll.co.ke`
(system-design.txt A.1, A.3). Two designs were considered:

1. Duplicate auth/session/billing handlers into every product API
   (`rentals-api`, `estates-api`, `waste-api` each run their own login,
   signup, activation, subscription webhook).
2. One `platform-api` binary owns identity and billing; every product API
   only VERIFIES a session against `willcoll_platform` and never issues one.

Duplicating auth across three-and-growing binaries means every auth bug fix,
every billing change, and the subscription webhook's idempotent-ingestion
logic (ADR-0004) has to be applied and deployed in lock-step across all of
them — the highest-risk code in the system, maintained in the most places.

## Decision

1. **A fourth binary, `platform-api`**, owns: signup/activation, sessions,
   account, staff, module activation/requests, plans/billing (including the
   subscription webhook), the SMS wallet, audit-log reads, and the Super
   Admin surface. It is the only binary that ever creates or destroys a
   session row. Product APIs (`rentals-api`, `estates-api`, `waste-api`)
   hold a `PLATFORM_DB_DSN` pool and read sessions/modules/roles/channels
   from it, but never write platform identity or billing tables except
   `payment_inbox` status and `audit_log` (system-design.txt E.4) — the two
   tables every product legitimately writes to as part of its own request
   handling.
2. **One session cookie, one domain.** SCS's session store lives in
   `willcoll_platform` (unchanged mechanism from ADR-0001 — server-side,
   instantly revocable). The cookie's `Domain` is set to `.willcoll.co.ke`
   (not a specific subdomain), `HttpOnly`, `Secure`, `SameSite=Lax` — safe
   because every host is same-site under that domain. Every frontend app
   calls its APIs with `credentials: 'include'`; every API's CORS allowlist
   is restricted to the willcoll app origins (system-design.txt A.4). A
   logout or a Super Admin suspension deletes one session row and every app
   is signed out at once, with no per-product sign-out to coordinate.
3. **Existing Rentals URL paths don't move.** `/v1/properties`,
   `/v1/pay/...` and the rest of Rentals' existing API surface stay on
   `rentals-api` (reachable at `api-rentals.willcoll.co.ke`) with unchanged
   request/response shapes — only the auth/account/billing/admin paths move
   to `platform-api` at `api.willcoll.co.ke`. This keeps the migration
   (build-phases.txt Phase 3) a subtraction from `rentals-api` plus an
   addition to `platform-api`, not a rewrite of Rentals' shipped surface.

## Consequences

- A session-verification bug now affects every product at once instead of
  one — the same trade every shared-infrastructure decision makes. The
  mitigation is that `platform-api` is small and focused (auth/billing only)
  and gets system-design.txt Part E.5's isolation/role/module tests applied
  to it directly, rather than duplicated per product.
- `platform-api`'s availability is a hard dependency for every product's
  login — consistent with ADR-0006's consequence that `willcoll_platform`'s
  uptime requirements now match the whole system's.
- Every product API's request-gate chain (system-design.txt A.5) always
  includes a `PLATFORM_DB_DSN` round-trip before it can even reach its own
  tenant transaction; this is an accepted latency cost of centralizing
  identity, not something the product APIs work around by caching sessions
  locally (a cached-but-since-revoked session would defeat ADR-0001's whole
  point).
- Frontend apps in `frontend/rentals`, `frontend/estates`, `frontend/waste`
  never render login/signup/billing UI themselves — a 401 redirects to
  `frontend/platform`'s login, which is the ONLY place those pages exist
  (system-design.txt A.9).
