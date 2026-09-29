-- Per-property print styling for tenant-facing PDFs (data.PrintTheme):
-- {"header_text","address_lines","phone","accent_color"}. NULL means "fall
-- back to the property's and landlord's own fields". No logo: PDFs are
-- rendered on demand and Willcoll stores no blobs.
ALTER TABLE properties ADD COLUMN IF NOT EXISTS print_theme jsonb;
