-- New and rotated unit QR codes now draw an 8-character token (unit-label
-- prefixed on the sticker/URL instead, e.g. "A1-K7QM2XH9"); tokens already
-- issued stay the legacy 12 characters and keep working unprefixed. Accept
-- exactly those two lengths, not any length 8 or above.
ALTER TABLE unit_qr_codes DROP CONSTRAINT unit_qr_codes_token_format;
ALTER TABLE unit_qr_codes ADD CONSTRAINT unit_qr_codes_token_format
    CHECK (token ~ '^[2-9A-HJKMNP-Z]{8}$' OR token ~ '^[2-9A-HJKMNP-Z]{12}$');
