-- Fails if two properties of one manager already share a receipt number.
ALTER TABLE receipts ADD CONSTRAINT receipts_tenant_id_receipt_no_key UNIQUE (tenant_id, receipt_no);