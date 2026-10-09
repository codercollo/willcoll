# C4.3 Route matrix review fixes

Save this file as docs/security/c4-matrix-fixes.md. It must survive /clear.

**Status: matrix NOT approved.** Fix the items below, regenerate, and stop.

Sources of truth:

- docs/security/authorization.md: actors, permissions, D1–D5
- docs/security/route-matrix.md: the current observed behaviour
- docs/security/c4-spec.md: how the matrix test works

## Rules (unchanged)

- **Git:** read-only only. Never add, commit, push, checkout, restore, reset, stash, merge, rebase, rm or mv.
- **Edits:** source edits only through the Edit/Write tools. Never sed -i, perl -pi, Python, heredocs or shell redirection into source files.
- **Gates:** run them one at a time, with no timeouts and nothing else competing for CPU.
  1. `make audit`
  2. `make test/race`
  3. `INTEGRATION=1 make test/integration` (0 skipped)
  4. `pnpm -r lint && pnpm -r typecheck && pnpm -r test`
- **Golden file:** every regeneration (`-update`) must be reviewed. List every changed cell in the report.
- **Stop:** stop at the end and report. Don't start Phase D.

---

## Why the matrix isn't approved

The matrix sends **empty request bodies**. For routes where the rule depends on _what is being changed_, an empty body proves nothing:

| Cell observed                                              | Why it's not proof                                                                           |
| ---------------------------------------------------------- | -------------------------------------------------------------------------------------------- |
| `PATCH /v1/staff/:id` → 2xx for owners, no step-up         | Nothing was changed. A role, module or property-access change must require step-up.          |
| `PATCH /v1/properties/:id` → 2xx for accountant            | No channel change was sent. Changing `payhero_channel_id` must stay owner-only with step-up. |
| (no row)                                                   | D3, "a staff owner can't grant the owner role", is never exercised.                          |
| `DELETE /v1/leases/:id/payers/:payerId` → 404 for everyone | Ambiguous: refused, or simply not found?                                                     |

Two business rules also need checking against how the firm actually works (fraud controls), and there are two gaps to close.

---

## 1. Exercise conditional rules with real request bodies

Add **variant rows** to the matrix test. Each variant is the same route with a specific, realistic body, named in the golden file and route-matrix.md like this:
`PATCH /v1/staff/:id [role change]`.

Each mutating variant runs on a fresh fixture, as the existing matrix does.

### 1a. `PATCH /v1/staff/:id` variants

| Variant                                | staff_owner  | firm_login | accountant / officer / viewer | super_admin |
| -------------------------------------- | ------------ | ---------- | ----------------------------- | ----------- |
| `[role change]` (officer → accountant) | step-up      | step-up    | 403                           | 403         |
| `[module_access change]`               | step-up      | step-up    | 403                           | 403         |
| `[property_access change]`             | step-up      | step-up    | 403                           | 403         |
| `[deactivate]`                         | step-up      | step-up    | 403                           | 403         |
| `[name only]` (no authority change)    | 2xx          | 2xx        | 403                           | 403         |
| `[grant owner]` (role → owner)         | **403** (D3) | step-up    | 403                           | 403         |

Also add `DELETE /v1/staff/:id` (or the existing removal route) with the same expectations as `[deactivate]`, if such a route exists. If removal is only via PATCH, say so.

After step-up is completed, each step-up cell must succeed. Add a second pass per variant with a fresh step-up, recorded as e.g. `2xx*`, where `*` means "after step-up".

### 1b. `PATCH /v1/properties/:id` variants

| Variant                                     | accountant | staff_owner     | firm_login      | officer / viewer | officer_scoped |
| ------------------------------------------- | ---------- | --------------- | --------------- | ---------------- | -------------- |
| `[channel change]` (new payhero_channel_id) | **403**    | step-up → 2xx\* | step-up → 2xx\* | 403              | 404            |
| `[channel echo]` (same value re-sent)       | 2xx        | 2xx             | 2xx             | 403              | 404            |
| `[name only]`                               | 2xx        | 2xx             | 2xx             | 403              | 404            |

### 1c. Other conditional routes

Find every other handler that applies a **field-level or value-dependent** check: a channel field, rent/deposit fields, role fields, status fields, and anything else using requireOwnerForChannelChange-style logic.

- Add variant rows for each one.
- List them in the report.

### Acceptance

- A test fails if any route with a field-level check has no variant row covering the protected field. Keep a reviewed list in code: `routesWithFieldChecks`.

---

## 2. Separate "reached the handler" from "stopped at the gate"

An ambiguous 404 or 422 must say whether authorization let the request through.

Add a test-only response header, or an equivalent internal hook enabled only in tests, that marks when the handler ran. Never enable it in production builds or in non-test environments, and add a test proving it's off by default.

Golden-file cells use these codes:

| Code                                       | Meaning                                                       |
| ------------------------------------------ | ------------------------------------------------------------- |
| `404h` / `422h` / `409h` / `400h` / `503h` | the handler ran and returned that code (authorization passed) |
| `404`                                      | refused by scope (out of the caller's properties or firm)     |
| `403`                                      | refused by permission                                         |
| `401`                                      | not authenticated                                             |
| `step-up`                                  | refused until the password is re-entered                      |
| `2xx*`                                     | succeeded after step-up                                       |

Fixture changes:

- `DELETE /v1/leases/:id/payers/:payerId` must use a **real fixture payer** on a lease in property A, so allowed principals get **2xx** and officer_scoped gets **404**.
- Review every other route where _all_ allowed principals currently get 404 or 422. Where a realistic fixture or body is feasible, add it so at least one allowed principal gets 2xx.
- List the routes where 2xx is infeasible, with the reason (for example, it needs an external service).

Update the legend at the top of route-matrix.md.

---

## 3. Officer-recorded payments must be pending until confirmed

**Risk:** a caretaker (officer) typing "cash received" is the classic way arrears get quietly cleared for a friend.

### 3a. Report current behaviour first

For `POST /v1/units/:id/rent/payments` as officer, report:

1. What gets created (tables, rows, status).
2. Whether the unit's balance or ledger changes immediately.
3. What `/v1/payments/review` and `/v1/payments/:id/confirm` do today.

### 3b. Required behaviour

- **Officer:** may record a manual (cash) payment. It is created with status **pending** and appears in `GET /v1/payments/review`. It must **not** post to the ledger or change any balance, arrears, report or receipt until confirmed.
- **Accountant / owner / firm login:**
  - May confirm a pending payment (`POST /v1/payments/:id/confirm`). Confirming posts to the ledger exactly once (idempotent), and records who confirmed it and when.
  - May reject a pending payment with a reason. Nothing posts.
  - Their own manual payments may post immediately, as today, unless the current design says otherwise. Report which.
- The recorder and confirmer are both shown in the payment's audit trail.
- **Self-confirmation:** if an officer is later promoted, they still can't confirm a payment they recorded themselves. Record this rule; implement it if cheap, otherwise leave it as a Phase 8 item.
- **Owner alert:** a rejected payment triggers an alert. A pending payment older than 48 hours shows a count on the dashboard.
- **Receipts:** no receipt or SMS goes to the tenant until confirmation.

### 3c. Tests (integration, real Postgres)

1. Officer records → payment is pending → balance unchanged → the payment appears in the review list.
2. Accountant confirms → ledger is credited once → balance updated → receipt issued.
3. Confirming twice → one ledger entry (the second call returns 409, or is a no-op; pick one and report).
4. Accountant rejects → nothing posted → owner alert enqueued.
5. officer_scoped can't record against a property B unit (404), and can't see property B pending payments.
6. Viewer can't record (403).

---

## 4. Officers must not change rent through unit routes (D1)

### 4a. Report

List every field accepted by:

- `PATCH /v1/units/:id`
- `POST /v1/properties/:id/units`
- `POST /v1/properties/:id/units/import`
- any other route that writes unit or lease money fields

### 4b. Required behaviour

- Any field that sets or changes **rent, deposit, service charge, water rate, garbage fee, or any other recurring or one-off charge amount** requires **accountant+**.
- If an officer's request includes such a field with a _changed_ value, return **403** and write nothing. Not a partial apply.
- Echoing the current value unchanged is allowed, so full-object forms keep working.
- Non-money fields stay officer-editable: unit label, notes, meter number, contacts and similar.
- **Unit creation by officer (`POST /v1/properties/:id/units`):**
  - allowed only if the money fields are absent or zero
  - otherwise 403
  - **or**, if that breaks the workflow, restrict unit creation to accountant+. Report which option you chose and why.
- Implement this as one shared field-guard helper used by every such route, not copy-pasted checks.

### 4c. Tests and matrix

- Matrix variants: `PATCH /v1/units/:id [rent change]`, `[label only]`, `[rent echo]`, and `POST /v1/properties/:id/units [with rent]`, `[no money fields]`.
  - Expected for officer: 403 / 2xx / 2xx / 403 / 2xx-or-403 (per your 4b choice).
  - Expected for accountant: 2xx (or 422h) across the board.
- Add these routes to `routesWithFieldChecks` (item 1c).

---

## 5. Viewers can see reports on screen

A landlord or auditor given a viewer account expects to see monthly reports.

- **Grant `reports.read` to viewer** for on-screen views only:
  - `GET /v1/properties/:id/reports/:period`
  - `GET /v1/properties/:id/reports/:period/checks`
- **Keep at accountant+:**
  - generate (`POST …/reports/:period/generate`)
  - confirm (`POST …/reports/:period/confirm`)
  - receipt download (`GET …/receipts/:period/download`)
  - every `.csv` / `.pdf` / export route
- **Response check:** if the report response includes data that viewers otherwise can't see (for example, full tenant phone numbers), mask it for viewer. Report what's in the payload.
- **Update docs/security/authorization.md:** the role matrix, plus a short "D6: viewer reads reports on screen" decision note.

### Matrix expectations

| Route                       | viewer | officer                |
| --------------------------- | ------ | ---------------------- |
| reports `GET` and `/checks` | 2xx    | 2xx (officer ⊇ viewer) |
| exports, generate, confirm  | 403    | 403                    |

---

## 6. Super Admin step-up

The Super Admin account is the most valuable target in the system.

**Require step-up** (re-enter the admin password; 5-minute window; same mechanism as C3.6) for:

- `POST /v1/admin/managers/:id/suspend`
- `POST /v1/admin/managers/:id/reinstate`
- `POST /v1/admin/unclaimed-receipts/:id/resolve`
- `POST /v1/admin/plans`
- `PATCH /v1/admin/plans/:id`
- `POST /v1/admin/plans/:id/archive`
- `POST /v1/admin/plans/:id/restore`

Requirements:

- Super Admin step-up verifies the **admin** credential, not a firm password, and is bound to the admin session.
- **Fix:** `POST /v1/sessions/step-up` currently returns 403 for super_admin. Make it work for Super Admin sessions.
- Each of these actions writes an audit entry: actor, IP, target, reason or note.
- Add these permissions to `auth.StepUpPermissions` and to the step-up drift test.
- Record **"Super Admin 2FA (TOTP), required before go-live"** as a Phase 9 item in docs/build-phases.txt.

### Matrix expectations

The cells above show `step-up` for super_admin, then `2xx*` (or `422h*` where the body is empty) after step-up.

---

## 7. Phase 5 backlog note: landlord payout details

Record under Phase 5 in docs/build-phases.txt. Do not build it now.

> **Landlord payout details** (bank account, M-Pesa number, paybill/till used for remittances). Any create or change is **owner-only + step-up + owner alert** (C3.7 outbox). Show the old and new values masked in the alert. A newly changed payout destination is held for 24 hours before the first remittance uses it (cooling-off period), unless the owner explicitly confirms. Redirecting payouts is the most common fraud in property management.

Also add a line to docs/security/authorization.md, section 4 (high-risk controls, planned).

---

## Regenerate, verify, report

1. Regenerate the golden file and route-matrix.md:
   `go test ./cmd/platform-api ./cmd/rentals-api -run RouteMatrix -update`
2. **Review the diff.** Every changed cell must be explained by one of items 1–6. Anything unexplained is a bug: investigate it and don't just accept it.
3. Run all four gates, one at a time.
4. Update the docs:
   - docs/security/authorization.md: D6; the payment-confirmation rule; the unit field guard; Super Admin step-up; planned payout controls.
   - docs/rentals/features-functionalities.txt: pending cash payments and confirmation; unit field rules.
   - docs/build-phases.txt: C4.3 ticked; Phase 5 and Phase 9 items added.

## Report (STOP)

Keep it to 20 lines or fewer, plus tables:

1. Gate results.
2. **Changed cells table:** route [variant] | principal | old → new | reason (item #).
3. Item 3a findings: current officer payment behaviour, before the fix.
4. Item 4a: the unit money fields found, and the 4b choice for unit creation.
5. Item 5: what's in the report payload, and what was masked for viewer.
6. Routes where 2xx is infeasible, with reasons (item 2).
7. Anything you could not do, and why.

Don't start Phase D.
