-- Phase 3.1: Organization white-label branding columns (spec §1b.1).

ALTER TABLE organizations ADD COLUMN slug TEXT;

-- Backfill pre-existing rows from brand_name. New rows get their slug from
-- the application at creation; this only makes historical rows satisfy the
-- NOT NULL + UNIQUE constraints added below.
UPDATE organizations
SET slug = trim(both '-' from regexp_replace(lower(brand_name), '[^a-z0-9]+', '-', 'g'));

UPDATE organizations
SET slug = 'org-' || substr(id::text, 1, 8)
WHERE slug IS NULL OR slug = '';

-- Resolve any collisions deterministically (existing rows only).
UPDATE organizations o
SET slug = o.slug || '-' || substr(o.id::text, 1, 8)
WHERE EXISTS (
    SELECT 1
    FROM organizations other
    WHERE other.id <> o.id AND other.slug = o.slug
);

ALTER TABLE organizations ALTER COLUMN slug SET NOT NULL;
ALTER TABLE organizations ADD CONSTRAINT organizations_slug_key UNIQUE (slug);

ALTER TABLE organizations ADD COLUMN logo_url TEXT;

ALTER TABLE organizations ADD COLUMN accent_key TEXT NOT NULL DEFAULT 'orange';
ALTER TABLE organizations ADD CONSTRAINT organizations_accent_key_check
    CHECK (accent_key IN ('orange', 'teal', 'indigo', 'crimson', 'forest'));

ALTER TABLE organizations ADD COLUMN sms_sender_id TEXT;
ALTER TABLE organizations ADD COLUMN email_from_name TEXT;
