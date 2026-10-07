DROP INDEX IF EXISTS admins_single_row_idx;
ALTER TABLE admins DROP COLUMN IF EXISTS is_singleton;
