-- Staff: sub-accounts under a manager (system-design.txt A.1). Roles are
-- fixed (owner/accountant/officer/viewer); module_access is a subset of the
-- manager's active modules (manager_modules, 000008) that this staff member
-- may open — enforced at the application layer, not by a DB constraint,
-- since it depends on another table's runtime state.
CREATE TABLE IF NOT EXISTS staff (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    manager_id uuid NOT NULL REFERENCES managers ON DELETE CASCADE,
    name text NOT NULL,
    email citext NOT NULL,
    phone text NULL,
    password_hash bytea NOT NULL,
    role text NOT NULL CHECK (role IN ('owner', 'accountant', 'officer', 'viewer')),
    module_access text[] NOT NULL DEFAULT '{}',
    status text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'suspended')),
    created_at timestamptz NOT NULL DEFAULT now(),
    version integer NOT NULL DEFAULT 1
);

-- Scoped per manager, not global: two firms may each have a staff member
-- using the same email address under separate accounts.
CREATE UNIQUE INDEX staff_manager_id_email_key ON staff (manager_id, email);
CREATE INDEX staff_manager_id_idx ON staff (manager_id);
