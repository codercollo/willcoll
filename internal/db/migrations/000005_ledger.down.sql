-- Reverse Phase 5.1.

DROP TABLE IF EXISTS ledger_entries;
DROP TABLE IF EXISTS ledger_transfers;
DROP TABLE IF EXISTS invoices;
DROP TABLE IF EXISTS ledger_accounts;

DROP TYPE IF EXISTS payment_method;
DROP TYPE IF EXISTS transfer_kind;
DROP TYPE IF EXISTS invoice_status;
DROP TYPE IF EXISTS invoice_kind;
DROP TYPE IF EXISTS account_owner_kind;
