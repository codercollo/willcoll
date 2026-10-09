-- The Super Admin's own trail (c4-matrix-fixes.md item 6). audit_log is
-- per firm (manager_id NOT NULL, RLS), so it cannot hold an action that belongs
-- to no firm (editing the global plan catalog) and it is the wrong place to
-- search for "everything the Super Admin did". Every step-up-gated admin action
-- and every admin step-up writes one row here: who, from where, which target,
-- and the reason or note. manager_id is the firm the action concerned, when it
-- concerned one. Append-only, like audit_log: a correction is a new row.
CREATE TABLE IF NOT EXISTS admin_audit_log (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    admin_id uuid NULL REFERENCES admins ON DELETE SET NULL,
    manager_id uuid NULL REFERENCES managers ON DELETE SET NULL,
    action text NOT NULL,
    entity_type text NOT NULL,
    entity_id uuid NULL,
    summary text NOT NULL,
    ip text NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS admin_audit_log_created_at_idx ON admin_audit_log (created_at DESC);

REVOKE UPDATE, DELETE ON admin_audit_log FROM willcoll_platform_app;
