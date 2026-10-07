CREATE EXTENSION IF NOT EXISTS citext;

CREATE TABLE IF NOT EXISTS managers (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    firm_name text NOT NULL,
    username text UNIQUE NOT NULL,
    email citext UNIQUE NOT NULL,
    phone text NOT NULL,
    password_hash bytea NOT NULL,
    status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'active', 'suspended')),
    activated_at timestamptz NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    version integer NOT NULL DEFAULT 1
);
