# 0006. Splitting the platform out of willcoll into willcoll_platform

Status: accepted

## Context

Today `managers`, `admins`, `staff` (new), `sessions`, `tokens`,
`subscription_plans`, `subscriptions` and `subscription_invoices` live in the
same `willcoll` database as every Rentals tenant table. Estates and Waste each
need their own database (system-design.txt Part A), but a manager still needs
ONE login and ONE subscription/module state across every product they use.
That only works if identity and billing live in a database no product owns —
`willcoll_platform` — read by every product API.

Two things make this risky rather than a simple `CREATE TABLE ... AS SELECT`:

1. `willcoll_rentals`'s tenant-scoped tables (`landlords`, `properties`, ...)
   currently `FOREIGN KEY (tenant_id) REFERENCES managers(id)`. Once
   `managers` moves to a different database, that FK is impossible — Postgres
   has no cross-database foreign keys, and system-design.txt Part E.4
   forbids them by rule anyway (no product may depend on another database's
   referential integrity).
2. Row-Level Security policies scope every tenant-scoped table by
   `app.tenant_id`. That's unaffected by the split (RLS doesn't care where
   `tenant_id` is validated), but losing the FK means Postgres itself no
   longer rejects a `tenant_id` that doesn't correspond to a real manager —
   validation moves entirely to the application layer.

## Decision

1. **`tenant_id` becomes a plain `uuid` column**, not a foreign key, in every
   product database. It is validated against `platform.managers` at the
   application layer (the session/auth middleware already resolves and
   trusts the manager id before any tenant-scoped query runs — see
   system-design.txt A.5's request-gate order). This is a deliberate
   loosening of a database-enforced guarantee to an application-enforced
   one, accepted because there is no alternative once the tables are in
   different databases.
2. **Every product API opens two connection pools**: `DB_DSN` for its own
   database, `PLATFORM_DB_DSN` for `willcoll_platform` (read sessions,
   modules, roles, channels, the payment inbox). Willcoll_platform's own
   RLS/grants are unaffected — `willcoll_app`/`willcoll_admin` per database,
   unchanged from ADR-0003.
3. **Cut-over is backup-first and reversible up to the point of no return**:
   a full `pg_dump` of `willcoll` immediately before migrating anything
   (`scripts/backup_before_split.sh`, which also restores that dump into a
   scratch database and asserts row counts match — a backup that hasn't
   been restored once is unverified). Platform tables are copied into
   `willcoll_platform` by an idempotent backfill tool (`cmd/devtools`,
   build-phases.txt 3d), re-runnable without duplicating rows. The
   `tenant_id` FK and the platform-owned tables are dropped from
   `willcoll_rentals` only in step 3i, the LAST step of Phase 3 — every
   earlier step can be rolled back by restoring the pre-split backup,
   because nothing destructive has happened to `willcoll_rentals` yet.
4. **An RLS isolation test is required before 3i's drop**, not just
   convenient: it proves one manager's session still cannot read another
   manager's rows now that `tenant_id` is no longer FK-backed (a plain
   `uuid` accepts any value the application hands it, so a bug that lets an
   unvalidated tenant_id through would otherwise surface silently as a
   cross-tenant data leak, not a constraint violation). See
   system-design.txt Part E.5's "RLS isolation" test.

## Consequences

- A `tenant_id` typo or a resolver bug that produces a wrong-but-well-formed
  UUID is no longer caught by the database — it's caught only by the RLS
  isolation test and by application-level validation against
  `platform.managers`. This is a real reduction in defense-in-depth, accepted
  as the cost of the platform split; there is no cross-database FK mechanism
  in Postgres to preserve it.
- Every product API now depends on `willcoll_platform` being reachable to
  authenticate any request — a platform database outage takes down every
  product's login, not just one. `willcoll_platform`'s availability
  requirements are therefore the same as the whole system's.
- The backfill tool and the pre-split backup are the only recovery path if
  the split goes wrong mid-flight; there is no automatic rollback once
  `willcoll_rentals`'s platform-owned tables are dropped in 3i.
