package mailer

// SMTPConfig is the Mailtrap-only SMTP configuration used in dev/staging.
// Production SMTP is Phase 11's concern and is deliberately not represented
// here yet.
type SMTPConfig struct {
	Host     string
	Port     string
	Username string
	Password string
	Sender   string
}
