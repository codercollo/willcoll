-- tenant_import_batches (spec §3.2a) — audit trail for bulk CSV unit/tenant
-- onboarding. One row per upload; append-only.

CREATE TABLE tenant_import_batches (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    property_id UUID NOT NULL REFERENCES properties(id) ON DELETE CASCADE,
    uploaded_by UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    filename TEXT NOT NULL,
    row_count INT NOT NULL,
    created_count INT NOT NULL,
    skipped_count INT NOT NULL,
    row_results JSONB NOT NULL,          -- per-row created/skipped + reason
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Append-only audit trail: only SELECT and INSERT policies exist.
ALTER TABLE tenant_import_batches ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_import_batches_select ON tenant_import_batches FOR SELECT USING (organization_id = current_setting('app.current_org_id')::uuid);
CREATE POLICY tenant_import_batches_insert ON tenant_import_batches FOR INSERT WITH CHECK (organization_id = current_setting('app.current_org_id')::uuid);
