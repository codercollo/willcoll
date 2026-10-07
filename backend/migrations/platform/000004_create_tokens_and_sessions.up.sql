-- Generic activation/password-reset tokens (internal/shared/auth's
-- GenerateTokenPlaintext/HashToken scheme). Belongs to exactly one manager
-- or one staff row, never both — staff invites/resets reuse the same
-- table and scope instead of a parallel one.
CREATE TABLE IF NOT EXISTS tokens (
    hash bytea PRIMARY KEY,
    manager_id uuid NULL REFERENCES managers ON DELETE CASCADE,
    staff_id uuid NULL REFERENCES staff ON DELETE CASCADE,
    expiry timestamptz NOT NULL,
    scope text NOT NULL CHECK (scope IN ('activation', 'password-reset')),
    CONSTRAINT tokens_exactly_one_owner CHECK (
        (manager_id IS NOT NULL)::int + (staff_id IS NOT NULL)::int = 1
    )
);

CREATE INDEX IF NOT EXISTS tokens_manager_id_idx ON tokens (manager_id) WHERE manager_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS tokens_staff_id_idx ON tokens (staff_id) WHERE staff_id IS NOT NULL;

-- alexedwards/scs/postgresstore session table. One cookie, shared by every
-- product's frontend (system-design.txt A.4): the payload carries a
-- principal-type discriminator (manager|admin|staff) and id, resolved by
-- internal/shared/auth on each request. Previously created ad hoc by scs's
-- own setup script in willcoll; brought under migrate here — a deliberate
-- tightening (docs/system-design.txt A.2), not a behavior change.
CREATE TABLE IF NOT EXISTS sessions (
    token text PRIMARY KEY,
    data bytea NOT NULL,
    expiry timestamptz NOT NULL
);

CREATE INDEX IF NOT EXISTS sessions_expiry_idx ON sessions (expiry);
