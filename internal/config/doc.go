// Viper loader — reads /configs/*.yaml + env overrides (DB DSN, server addr,
// PASETO key, SMTP host/port/creds/sender, SMS gateway keys,
// intasend.publishable_key/secret_key/webhook_secret/environment — sandbox in
// dev/staging, live only in production,
// scoring.sidecar_base_url/scoring.shared_secret for the ml-sidecar, spec §13.3).
package config
