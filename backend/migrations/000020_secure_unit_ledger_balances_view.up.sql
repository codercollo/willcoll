-- A view runs with its OWNER's privileges by default. unit_ledger_balances
-- is owned by the migration role (a superuser locally), which bypasses RLS,
-- so reading it as willcoll_app returned every tenant's balances.
-- security_invoker (PostgreSQL 15+) makes the view check RLS and grants as
-- the querying role instead, so the ledger_accounts / ledger_entries
-- tenant_isolation policies apply through it.
ALTER VIEW unit_ledger_balances SET (security_invoker = true);
