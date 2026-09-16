// Organization-level white-label settings (spec §1b). Owns the ONLY writes to
// organizations.brand_name/logo_url/accent_key/sms_sender_id/email_from_name —
// no handler outside this package touches those columns.
package branding
