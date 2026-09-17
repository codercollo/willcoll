-- Per-Organization SMS template overrides (spec Phase 8.2). template_key is
-- one of the six fixed *.tmpl filenames internal/notify already renders —
-- enforced in Go (notify.Templates), not by a DB CHECK, since the closed set
-- lives in code alongside the .tmpl files themselves. Absence of a row here
-- means "use the shipped default" (internal/notify/sms.go's templateBody).
CREATE TABLE sms_template_overrides (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    template_key TEXT NOT NULL,
    body TEXT NOT NULL,
    updated_by UUID NOT NULL REFERENCES users(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (organization_id, template_key)
);

ALTER TABLE sms_template_overrides ENABLE ROW LEVEL SECURITY;
CREATE POLICY sms_template_overrides_select ON sms_template_overrides FOR SELECT USING (organization_id = current_setting('app.current_org_id')::uuid);
CREATE POLICY sms_template_overrides_insert ON sms_template_overrides FOR INSERT WITH CHECK (organization_id = current_setting('app.current_org_id')::uuid);
CREATE POLICY sms_template_overrides_update ON sms_template_overrides FOR UPDATE USING (organization_id = current_setting('app.current_org_id')::uuid) WITH CHECK (organization_id = current_setting('app.current_org_id')::uuid);
CREATE POLICY sms_template_overrides_delete ON sms_template_overrides FOR DELETE USING (organization_id = current_setting('app.current_org_id')::uuid);
