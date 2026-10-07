-- Willcoll has exactly one Super Admin (system-design.txt A.1), provisioned
-- from .env at startup. is_singleton can only be true (the CHECK) and must
-- be unique (the index), so a second row can never exist.
CREATE TABLE IF NOT EXISTS admins (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name text NOT NULL,
    email text UNIQUE NOT NULL,
    password_hash bytea NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    is_singleton boolean NOT NULL DEFAULT true CHECK (is_singleton)
);

CREATE UNIQUE INDEX admins_single_row_idx ON admins (is_singleton);
