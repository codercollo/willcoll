-- Phase 9.2: append-only audit log. RLS is direct on organization_id; only
-- SELECT and INSERT policies exist, matching the ledger's immutability rule.

CREATE TABLE audit_log (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    property_id UUID REFERENCES properties(id) ON DELETE SET NULL,
    actor_id UUID REFERENCES users(id) ON DELETE SET NULL,
    action TEXT NOT NULL,
    entity_type TEXT NOT NULL,
    entity_id UUID,
    metadata JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

ALTER TABLE audit_log ENABLE ROW LEVEL SECURITY;
CREATE POLICY audit_log_select ON audit_log FOR SELECT USING (organization_id = current_setting('app.current_org_id')::uuid);
CREATE POLICY audit_log_insert ON audit_log FOR INSERT WITH CHECK (organization_id = current_setting('app.current_org_id')::uuid);
