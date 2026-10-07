ALTER TABLE unit_qr_codes DROP CONSTRAINT unit_qr_codes_token_format;
ALTER TABLE unit_qr_codes ADD CONSTRAINT unit_qr_codes_token_format
    CHECK (token ~ '^[2-9A-HJKMNP-Z]{10,}$');
