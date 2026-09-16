package mailer

import "context"

// Mailer is the seam between the Mailtrap sandbox used in dev/staging and the
// real transactional SMTP provider that replaces it in Phase 11.
type Mailer interface {
	Send(ctx context.Context, recipient, templateFile string, data any) error
}

// Service implements Mailer. It holds the SMTP configuration; templates are
// embedded by mailer.go.
type Service struct {
	config SMTPConfig
}

// NewService constructs a mailer Service backed by smtpConfig.
func NewService(smtpConfig SMTPConfig) *Service {
	return &Service{config: smtpConfig}
}

var _ Mailer = (*Service)(nil)
