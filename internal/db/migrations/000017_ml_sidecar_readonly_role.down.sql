REVOKE ALL PRIVILEGES ON properties, ledger_entries, ledger_accounts, invoices, leases, units, meter_readings, property_scores FROM ml_sidecar_readonly;
REVOKE USAGE ON SCHEMA public FROM ml_sidecar_readonly;
DROP ROLE IF EXISTS ml_sidecar_readonly;
