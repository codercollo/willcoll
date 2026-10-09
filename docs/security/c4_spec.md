# C4 spec: C3 loose ends, authorization backfill and route × role matrix

Save this file as docs/security/c4-spec.md. It must survive /clear.

The source of truth is docs/security/authorization.md. Cite its sections; don't re-derive them.

## Rules (unchanged)

- Git: read-only only. Never add, commit, push, checkout, restore, reset, stash, merge, rebase, rm or mv.
- Stopping: stop at each STOP marker and report.
- Gates: run them one at a time, with no timeouts and nothing else competing for CPU.
  1. make audit (lint, vet, staticcheck)
  2. make test/race
  3. INTEGRATION=1 make test/integration (0 skipped)
  4. pnpm -r lint && pnpm -r typecheck && pnpm -r test
- Edits: source edits only through the Edit/Write tools. Never sed -i, perl -pi, Python, or shell redirection into source files.

---

## C4.0 C3 loose ends

### 1. Staging credentials mismatch

The PayHero webhook secret and the Super Admin login from deploy/local/.env returned 401 against the running stack.

- Find the cause. Candidates: stale containers, the seed not creating the Super Admin, an env var name mismatch, or an image built before the change.
- Fix the cause, so `make staging/reset CONFIRM=yes && make staging/up && make staging/seed` gives a stack that accepts exactly what deploy/local/.env says.
- Add a `make staging/smoke` target that checks, over HTTPS:
  - the Super Admin can log in
  - a signed fake webhook is accepted
  - a firm owner can log in
  - every health endpoint is green

### 2. Unclaimed receipt, end to end on staging

1. Send a signed webhook for an unclaimed channel.
2. Check the row exists in unclaimed_receipts.
3. Check the System page shows the open count and amount.
4. As Super Admin, resolve it with a note.
5. Check the second resolve fails.
6. Report each result.

### 3. Owner-alert durability (transactional outbox)

An alert enqueued after the commit can be lost if the process dies between the two. Alerts are the anti-fraud control, so they must not be lost.

- Platform actions (staff role, module, property and removal changes; channel changes): enqueue with EnqueueIn in the same transaction as the action. Thread the querier through StaffModel and the channel model as needed.
- Rentals actions (charge reversals) live in a different database, so they can't share a transaction with the platform outbox:
  - Add a rentals-side alert_outbox table.
  - Write to it in the same transaction as the reversal.
  - A relay job moves pending rows to the platform outbox and marks them relayed, idempotently, keyed by event id.
- Tests:
  - A rollback leaves no alert on either side.
  - A crash after commit but before the relay still delivers once the relay runs.
  - A duplicate relay sends no duplicate email.

STOP: report C4.0 in 10 lines or fewer.

---

## C4.1 Authorization backfill

Goal: every existing principal ends up in a valid, least-privilege state that matches the approved matrix, with no surprise changes for real users.

### 1. Dry run first

Add `go run ./cmd/devtools authz-backfill --dry-run`. It reports counts, and lists IDs only (no names or phones), for:

- staff with a NULL or invalid role. Proposed change: viewer.
- staff with module_access entries that are:
  - unknown or misspelled, for example "Rentals"
  - duplicates
  - not an active module of the firm
    Proposed change: normalize to lowercase, dedupe, and drop lapsed modules, but only report lapsed ones (M8 rule: don't break existing users).
- staff with malformed property_access JSON, or IDs that point to deleted or other-firm properties. Proposed change: drop those IDs, and never widen access.
- staff that are inactive or removed but still have live sessions. Proposed change: revoke the sessions.
- staff whose firm doesn't exist (orphans). Proposed change: deactivate them and revoke their sessions.
- firm logins without an implicit owner. Expected 0; report if not.

Rule: the backfill may only narrow or keep access, never widen it. A test must enforce this.

### 2. Apply

- Make it a migration (or migrations) plus devtools `authz-backfill --apply` for the parts that need cross-database checks.
- Idempotent: a second run reports 0 changes.
- Each change writes one audit row: actor_type=system, actor_name=authz-backfill, with the reason.

### 3. Tests

Integration test against messy seeded data (one case of every category above):

- After apply, everything is normalized.
- A second apply makes 0 changes.
- No principal gained any permission or scope. Compare effective permissions and scopes before and after.

### 4. Run it against local staging

Run the dry run, then apply, then the dry run again (expect 0). Report the counts.

---

## C4.2 Golden route × role matrix

### Principals to test

| Principal          | Notes                                             |
| ------------------ | ------------------------------------------------- |
| anonymous          |                                                   |
| tenant pay token   | valid, for unit A1                                |
| viewer             |                                                   |
| officer            | unscoped                                          |
| officer scoped     | property A only, requesting a property B resource |
| accountant         |                                                   |
| staff owner        |                                                   |
| firm login         |                                                   |
| Super Admin        |                                                   |
| service credential |                                                   |

### The test

For every registered route in platform-api and rentals-api, a table-driven test calls the route as each principal above, with realistic path params from a seeded fixture. It records the status code, plus "step_up_required" where that applies.

Expected outcomes are stored in a golden file: backend/internal/shared/auth/testdata/route_matrix.golden. The test fails on any difference. Regenerate only with `go test ... -update`, and every regeneration must be reviewed.

### Human-readable copy

Generate docs/security/route-matrix.md from the golden file. It has one table per API:

- Rows: routes, as method + path.
- Columns: the principals.
- Cells: 2xx/401/403/404/422/step-up.

Group the rows by resource.

### Drift checks (tests)

- Every permission in the catalog appears in docs/security/authorization.md.
- Every route in the golden file has a permission, or an entry in the public allowlist.
- The step-up permission list in the C3 test matches the step-up column in the golden file.

### Invariants (asserted in code, not only in the golden file)

- Viewer: never 2xx on POST, PUT, PATCH or DELETE.
- Viewer: never 2xx on export routes (exports.create).
- Super Admin: never 2xx on firm ledger routes.
- Scoped officer: always 404 on property B resources.
- Tenant token: only 2xx on pay routes for its own unit.
- Service credential: only 2xx on /internal/v1/\*. User sessions: never 2xx there.

---

## Docs

- docs/security/authorization.md: record the backfill rules, the matrix test, and the outbox design.
- docs/build-phases.txt: tick C1 through C4.

STOP: report in 15 lines or fewer, with:

- the dry-run counts and the apply counts
- the path of route-matrix.md
- every route where the observed status differs from authorization.md, if any

Don't start Phase D.
