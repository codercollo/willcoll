CREATE TABLE IF NOT EXISTS admins (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name text NOT NULL,
    email text UNIQUE NOT NULL,
    password_hash bytea NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
