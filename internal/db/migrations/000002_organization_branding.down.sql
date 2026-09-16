-- Reverse Phase 3.1.

ALTER TABLE organizations DROP CONSTRAINT IF EXISTS organizations_accent_key_check;
ALTER TABLE organizations DROP CONSTRAINT IF EXISTS organizations_slug_key;

ALTER TABLE organizations DROP COLUMN IF EXISTS email_from_name;
ALTER TABLE organizations DROP COLUMN IF EXISTS sms_sender_id;
ALTER TABLE organizations DROP COLUMN IF EXISTS accent_key;
ALTER TABLE organizations DROP COLUMN IF EXISTS logo_url;
ALTER TABLE organizations DROP COLUMN IF EXISTS slug;
