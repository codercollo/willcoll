# C3 spec — system actors, tenant actors, step-up auth, owner alerts

Source of truth: docs/security/authorization.md (actors, D5). Cite sections; don't re-derive them.

## Rules (unchanged)

- Git: read-only only. Never add, commit, push, checkout, restore, reset, stash, merge, rebase, rm or mv.
- Stopping: stop at each STOP marker and report.
- Gates: run these with no timeouts.
  - make audit
  - make test/race
  - INTEGRATION=1 make test/integration (0 skipped)
  - pnpm -r lint && pnpm -r typecheck && pnpm -r test
- Edits: use the edit tools, never Python rewrites, so line endings stay LF.

## C3.1 Tenant (pay-link) actor — DONE (see report of 2026-10-09)

- Pay tokens are bound to a lease, and the live lease is checked on every pay route.
- A token used on another unit's pay URL returns 404.
- OTP limits:
  - 5 attempts per code, plus the per-phone and per-unit caps.
  - 60 OTP requests per hour per IP.
  - A per-firm daily ceiling, PAY_FIRM_DAILY_OTPS.
- Pay route limits:
  - 20 requests/s, burst 40, per IP.
  - Status polling: 1 request/s, burst 3, per intent.

## C3.2 Webhook actor (PayHero)

- Signature: verify with a constant-time compare. A bad or missing signature returns 401 and is logged, and nothing is written.
- Replay: re-delivering the same receipt is idempotent. Report whether PayHero sends a timestamp. If it does, reject deliveries outside a tolerance window.
- Channel: channel_id must resolve to a claimed channel. The posting product and firm come from the claim, never from the payload. An unknown or unclaimed channel goes to the inbox without posting, and raises the existing alarm.
- Tests: one for each rule.

## C3.3 Service-to-service actor (product → platform)

- Credential: usage reporting, health and any other product→platform calls authenticate with a per-service credential. It comes from env, is checked with a constant-time compare, and is rotatable by accepting a current and a previous value.
- Endpoint restrictions:
  - These endpoints accept only the service credential, never user sessions.
  - They are reachable only on the internal network. They are not routed by Caddy, and the Caddyfiles block them.
- Secrecy: never expose the credential to the frontend or in logs.
- Tests:
  - A missing credential is refused.
  - A wrong credential is refused.
  - The previous (rotated) credential is still accepted.
  - A user session can't call the service endpoints.

## C3.4 Background jobs

- Identity: jobs and sweepers act as an explicit system actor in audit_log, with actor_type=system and actor_id set to the job name.
- Scope: they set tenant context for each firm they touch, and never run with a leftover tenant or property setting.
- Test: a job that processes firm A and then firm B writes each firm's rows correctly, with no scope carry-over.

## C3.5 Denied-request logging

- What: 401, 403 and scope 404 denials are logged as structured lines with actor, permission, route, IP and request id.
- Volume: sample or rate-limit per actor, so an attacker can't flood the logs.
- Where: logs only, never audit_log.

## C3.6 Step-up authentication (D5)

- Endpoint: POST /v1/auth/step-up re-enters the password.
  - The firm login uses the firm password; staff use their own.
  - On success the current session is step-up-verified for 5 minutes.
  - It is rate limited, and failures are audited.
- Protected actions: the requireStepUp middleware covers:
  - payment_channels.manage
  - staff create, role change, property_access change, module_access change and removal
  - charges.reverse
  - any future payments.reverse. A test lists these permissions and fails if one is missing.
- Response: without a fresh step-up, return 403 with the machine-readable code "step_up_required".
- Lifetime: step-up is bound to the session id, and cleared on logout, revocation or password change.
- Frontend: kept minimal and placed in layers/base, so both apps get it.
  - The API client detects step_up_required and shows a password modal.
  - It calls step-up, then retries the original request once.
  - Cancel leaves the action undone and shows a clear message.
- Tests:
  - Backend: fresh, expired and missing step-up; session binding; cleared on logout.
  - Frontend: the modal shows; the retry succeeds; the cancel path.

## C3.7 Owner alerts (D5)

- Triggers: email the firm owner on:
  - a payment channel change
  - any charge reversal
  - a staff role, module_access or property_access change
  - staff removal
- Content:
  - what happened
  - the actor's name and role
  - the IP
  - the target (staff, property or channel)
  - the amount via moneyfmt, where relevant
  - Nairobi time
  - a link to the record
  - no secrets, and phone numbers masked
- Delivery:
  - Sent asynchronously through a DB-backed outbox with retries.
  - A failed email never rolls back the action.
  - Failures are logged and shown on the System page.
  - Idempotent per event.
- Tests: use the fake mailer. Locally, alerts can be checked in Mailpit.

## C3.8 Open items carried from C2

- M8: on staff create or update, module_access must be a subset of the firm's active modules (manager_modules). Otherwise return 422. Test it.
- The "only the firm login should be able to …" item:
  - Find it in the mismatch list in docs/security/authorization.md, resolve it, and test it.
  - Also confirm D3 (only the firm login can grant the owner role) is enforced and tested.

## Docs

- docs/security/authorization.md: update section 4 to describe the high-risk controls as built.
- CLAUDE.md: add the step-up and outbox conventions.
- CLAUDE.md: fix the frontend gate command to `pnpm -r lint && pnpm -r typecheck && pnpm -r test`.

## Manual verification in local staging (make staging/up; report results)

1. As owner, reverse a charge. A password modal should appear, the reversal should succeed, and the alert email should arrive in Mailpit (http://127.0.0.1:8025).
2. Wait more than 5 minutes and reverse again. You should be asked for the password again.
3. As accountant, reverse a charge. The owner should get an alert naming the accountant and their IP.
4. A tenant pay link for unit A must not be able to fetch unit B's receipt.

STOP: report in 15 lines or fewer, plus the test list.
