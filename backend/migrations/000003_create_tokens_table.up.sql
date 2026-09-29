CREATE TABLE IF NOT EXISTS tokens (
    hash bytea PRIMARY KEY,
    manager_id uuid NOT NULL REFERENCES managers ON DELETE CASCADE,
    expiry timestamptz NOT NULL,
    scope text NOT NULL CHECK (scope IN ('activation', 'password-reset'))
);

CREATE INDEX IF NOT EXISTS tokens_manager_id_idx ON tokens (manager_id);

-- alexedwards/scs/postgresstore session table (managers + admins share this
-- one cookie-session table; the session payload itself carries a
-- principal-type discriminator, see system-design.txt 3.1).
CREATE TABLE IF NOT EXISTS sessions (
    token text PRIMARY KEY,
    data bytea NOT NULL,
    expiry timestamptz NOT NULL
);

CREATE INDEX IF NOT EXISTS sessions_expiry_idx ON sessions (expiry);
