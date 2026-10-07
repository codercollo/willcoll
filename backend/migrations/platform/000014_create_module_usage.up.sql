-- Billable usage, reported by each product, priced by platform
-- (system-design.txt A.7's DECISION). One row per (manager, product):
-- Rentals counts active units, Estates counts units, Waste counts active
-- customers. Each product API upserts its own row whenever the count
-- changes, plus a daily re-count job as a safety net (build-phases.txt
-- 3e). platform-api's per-unit pricing reads this table; it never queries
-- a product database directly, and it never calls a product API
-- (E.4 — a product API may write only its own module_usage row, plus
-- payment_inbox status and audit_log).
--
-- RLS'd like every other manager-scoped platform table: a product API
-- writes this through its own tenant transaction (SET LOCAL
-- app.tenant_id = the manager id), same as any other tenant-scoped write.
-- Grants come from 000013's ALTER DEFAULT PRIVILEGES (new tables already
-- get full DML for willcoll_platform_app, SELECT for willcoll_platform_admin).
CREATE TABLE IF NOT EXISTS module_usage (
    manager_id uuid NOT NULL REFERENCES managers ON DELETE CASCADE,
    product text NOT NULL CHECK (product IN ('rentals', 'estates', 'waste')),
    billable_count int NOT NULL CHECK (billable_count >= 0),
    reported_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (manager_id, product)
);

ALTER TABLE module_usage ENABLE ROW LEVEL SECURITY;
ALTER TABLE module_usage FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON module_usage
    USING (manager_id = current_setting('app.tenant_id')::uuid);
