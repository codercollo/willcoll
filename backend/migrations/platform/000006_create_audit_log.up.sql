-- Append-only audit trail (system-design.txt A.1, A.8): every mutation in
-- every product writes one row here, in addition to whatever it does in
-- its own database. staff_id is null when the Super Admin or the system
-- (a scheduled job) performed the action rather than a manager's staff
-- member. product is null for platform-only actions (e.g. staff created).
CREATE TABLE IF NOT EXISTS audit_log (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    manager_id uuid NOT NULL REFERENCES managers ON DELETE CASCADE,
    staff_id uuid NULL REFERENCES staff ON DELETE SET NULL,
    product text NULL CHECK (product IS NULL OR product IN ('rentals', 'estates', 'waste')),
    action text NOT NULL,
    entity_type text NOT NULL,
    entity_id uuid NULL,
    summary text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX audit_log_manager_id_created_at_idx ON audit_log (manager_id, created_at DESC);
