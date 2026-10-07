-- Willcoll has exactly one Super Admin (system-design.txt 1.1), provisioned
-- from .env at startup. is_singleton can only be true (the CHECK) and must
-- be unique (the index), so a second row can never exist. The API's
-- UpsertAdmin conflicts on this column, so changing ADMIN_EMAIL re-points
-- the one admin row rather than adding another.
--
-- Fails if the table already holds more than one admin: delete the extras
-- first.
ALTER TABLE admins ADD COLUMN is_singleton boolean NOT NULL DEFAULT true CHECK (is_singleton);
CREATE UNIQUE INDEX admins_single_row_idx ON admins (is_singleton);
