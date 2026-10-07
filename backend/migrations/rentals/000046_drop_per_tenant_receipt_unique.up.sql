-- Receipts are numbered per property (receipt_counters), and 000035 added
-- UNIQUE (tenant_id, property_id, receipt_no) but left the old per-tenant
-- constraint from 000015 in place, so a second property's receipt 1 collided.
ALTER TABLE receipts DROP CONSTRAINT IF EXISTS receipts_tenant_id_receipt_no_key;