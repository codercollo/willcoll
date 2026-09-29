# ADR 0002 — Append-only ledger with storno corrections

- Status: Accepted
- Date: 2026-09-23
- Spec: `system-design.txt` §3.3, §3.4, §3.8, §3.2.1

## Context

Willcoll replaces a hand-ruled paper ledger in which nothing is ever erased:
a mistake stays on the page and the correction is written beside it. Money
owed and money paid must be auditable end to end, and two PayHero webhooks
for the same unit can arrive concurrently.

## Decision

1. **Append-only tables.** `charges`, `transaction_headers`,
   `ledger_entries` and `payment_allocations` are INSERT-only. They have no
   `updated_at` and no `version` column: with no UPDATEs there is nothing
   for optimistic locking to protect.
2. **Enforced by the database, not just convention.** Migration
   `000018_grant_app_roles` revokes `UPDATE` and `DELETE` on those four
   tables from `willcoll_app`, the role the API connects as. An accidental
   `UPDATE ledger_entries ...` fails with `permission denied`.
3. **Storno corrections.** A wrong posting is fixed by a `REVERSAL`
   `transaction_header` that contains the mirrored entries (DEBIT and
   CREDIT swapped), followed by a new correct entry. A wrong charge is
   fixed by a negative-amount `manual` charge that references the
   original charge's id.
4. **Balances are derived, never stored.** The `unit_ledger_balances` view
   (migration 000012) sums `ledger_entries` per unit and type. It is backed
   by the index on `ledger_entries (ledger_account_id, created_at)`.
5. **Invariants.** `ledger_entries.amount > 0` (the `direction` column
   carries the sign). Each `transaction_headers.idempotency_key` is unique.
   Payment postings are zero-sum, asserted in Go inside the posting
   transaction (§3.8).

## LEASE_START deposit posting: migration ordering

§3.2.1 requires that creating a lease also posts its `RENT_DEPOSIT` and
`WATER_DEPOSIT` charges, in the same transaction. The pieces it needs
(`ledger_accounts` in 000008, `charges` in 000010, `transaction_headers`
and `ledger_entries` in 000011) don't exist yet when `leases` is created in
000007.

**Chosen split:** migration 000007 creates only the bare `leases` and
`lease_payers` tables (with `version` and the `one_active_lease_per_unit`
partial unique index). There is **no Postgres trigger**. The posting happens
in the application layer, in `internal/data/leases.go` `LeaseModel.Insert`.
In one `BeginTx` transaction it:

1. inserts the lease,
2. gets or creates the unit's `RENT_DEPOSIT` / `WATER_DEPOSIT`
   `ledger_accounts` row,
3. inserts one `lease_start` charge per non-zero deposit,
4. inserts one `LEASE_START` `transaction_header` whose idempotency key is
   `lease.id` (so a lease can post deposits only once), with a DEBIT
   `ledger_entry` for each charge.

Any failure rolls the whole transaction back, including the lease itself.

**Why not a trigger:**

- Every other ledger posting (rent, water and garbage runs, payment posting,
  reversals) is a Go function wrapped in `BeginTx` (§3.8). Keeping this one
  there means one posting code path, one place for SERIALIZABLE and
  retry-on-40001 handling, and one set of tests.
- A trigger would need `created_by` (the acting manager) and the request's
  tenant context. Go already has both; a trigger would have to read them
  from session settings.
- Posting logic in Go can be unit-tested with the gomock Querier (§9). A
  trigger can only be tested against a live database.
- The migrations stay in plain dependency order, with no trigger migration
  squeezed in after 000011 that points back at 000007.

**Consequence:** a `leases` row inserted by hand in `psql` posts no
deposits. That's acceptable: only the API writes to `leases`, and the
manual correction path is a storno adjustment anyway.
