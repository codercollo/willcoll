# ADR 0001: SCS server-side sessions, not JWT or bearer tokens

- Status: Accepted
- Date: 2026-09-23
- Spec: `system-design.txt` §4.4, §4.5, §1.1

## Context

The manager dashboard and the Super Admin pages are served to a Nuxt app on
a trusted origin, which calls the API with `credentials: 'include'`. This is
a dashboard, not a public or partner API. Losing access has to take effect
immediately: when a firm's subscription lapses, or the Super Admin suspends
it, its sessions must stop working now, not when a token expires.

Let's Go Further (ch.15) issues stateful bearer tokens from a `tokens`
table. The other common choice is a stateless JWT or PASETO.

## Decision

Use `alexedwards/scs` server-side sessions stored in Postgres (the
`sessions` table, migration 000003).

- **One session table for managers and the admin.** The session payload
  holds `principal_type` (`manager` or `admin`) and `principal_id`. There's
  no authentication-token table.
- **Login.** `POST /v1/sessions` (managers) and `POST /v1/admin/sessions`
  (the Super Admin) check the password with bcrypt, **renew the session
  token** (defeating session fixation), then store the principal. They are
  separate endpoints, so a manager and the admin can't be mistaken for one
  another. An unknown email still costs one bcrypt comparison, so response
  timing doesn't reveal which emails are registered.
- **Logout.** `DELETE /v1/sessions` destroys the session: the row is
  deleted and the cookie cleared.
- **Every request.** `authenticate` (cmd/api/middleware.go) loads the
  principal from the session, and reloads the manager or admin from the
  database. Route-group guards then apply RBAC (§4.5):
  `requireActivatedManager` for the dashboard (and it refuses a suspended
  or unactivated manager on the spot), `requireAdmin` for `/v1/admin/*`.
- **Cookie.** `willcoll_session`: HttpOnly, SameSite=Lax, Secure everywhere
  except development (Caddy terminates TLS), 12h lifetime, 2h idle timeout.
- **Instant revocation.** Suspending a firm
  (`POST /v1/admin/managers/:id/suspend`) destroys every one of its
  sessions right away. SCS stores sessions as encoded blobs, so this walks
  them with `SessionManager.Iterate`, which is fine at this platform's size.
  Subscription lapse will use the same path.

## Tenant scope (the RLS transaction)

The build plan (ch.15.3) has the session middleware open one request-wide
transaction and run `SET LOCAL app.tenant_id` in it. Instead, `authenticate`
puts the manager's id on the request context, and every data-layer call
runs in its own `db.Store.ExecTenantTx`, which opens a SERIALIZABLE
transaction and sets `app.tenant_id` as its first statement. RLS applies
the same way either way. Per-call transactions were chosen because:

- a serialization failure (40001) can be retried for just that unit of
  work (§3.8); a request-wide transaction can't safely re-run the handler,
  which may already have written a response or queued an email;
- transactions don't stay open while a handler waits on anything slow
  (bcrypt, email, PayHero), so they don't hold pooled connections;
- multi-row mutations (lease plus deposit postings, billing runs) are still
  atomic, because each is one data-layer method, which is one transaction.

## Consequences

- One database read per authenticated request (the principal reload), in
  exchange for instant suspension and revocation. Acceptable at dashboard
  traffic levels.
- The sessions table grows with active logins; postgresstore deletes
  expired rows every 5 minutes.
- If a public or partner API is opened later, it should get PASETO tokens
  for that surface only (§4.4), alongside these sessions rather than
  replacing them.
