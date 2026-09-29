// Package mailer sends the API's transactional emails (activation /
// welcome, password reset) from templates embedded in the binary, following
// Greenlight ch.13.
//
// Each template file defines three named templates: "subject",
// "plainBody" and "htmlBody". Every email goes out as multipart/alternative
// with a plain-text part and an HTML part styled with the Willcoll design
// tokens (design-tokens.txt).
package mailer

import (
	"bytes"
	"embed"
	htmltemplate "html/template"
	"log/slog"
	texttemplate "text/template"
	"time"

	"github.com/wneessen/go-mail"
)

//go:embed "templates"
var templateFS embed.FS

// Mailer sends one templated email. Handlers depend on this interface so
// tests can substitute a fake.
type Mailer interface {
	Send(recipient, templateFile string, data any) error
}

// Message is a rendered email.
type Message struct {
	Subject   string
	PlainBody string
	HTMLBody  string
}

// Render executes templateFile with data. The subject and plain-text body
// use text/template (so "A & B Estates" isn't HTML-escaped into the subject
// line); the HTML body uses html/template, which escapes data for HTML.
func Render(templateFile string, data any) (Message, error) {
	path := "templates/" + templateFile

	textTmpl, err := texttemplate.New("").ParseFS(templateFS, path)
	if err != nil {
		return Message{}, err
	}
	htmlTmpl, err := htmltemplate.New("").ParseFS(templateFS, path)
	if err != nil {
		return Message{}, err
	}

	var subject, plainBody, htmlBody bytes.Buffer
	if err := textTmpl.ExecuteTemplate(&subject, "subject", data); err != nil {
		return Message{}, err
	}
	if err := textTmpl.ExecuteTemplate(&plainBody, "plainBody", data); err != nil {
		return Message{}, err
	}
	if err := htmlTmpl.ExecuteTemplate(&htmlBody, "htmlBody", data); err != nil {
		return Message{}, err
	}

	return Message{Subject: subject.String(), PlainBody: plainBody.String(), HTMLBody: htmlBody.String()}, nil
}

// SMTPMailer sends through an SMTP server.
type SMTPMailer struct {
	client *mail.Client
	sender string
}

// NewSMTP returns a mailer for the given SMTP server. Port 465 uses
// implicit TLS; any other port upgrades with STARTTLS when the server
// offers it. The auth mechanism is negotiated with the server.
func NewSMTP(host string, port int, username, password, sender string) (*SMTPMailer, error) {
	opts := []mail.Option{
		mail.WithPort(port),
		mail.WithTimeout(5 * time.Second),
		mail.WithTLSPortPolicy(mail.TLSOpportunistic),
	}
	if port == 465 {
		opts = append(opts, mail.WithSSLPort(false))
	}
	if username != "" {
		opts = append(opts,
			mail.WithSMTPAuth(mail.SMTPAuthAutoDiscover),
			mail.WithUsername(username),
			mail.WithPassword(password),
		)
	}

	client, err := mail.NewClient(host, opts...)
	if err != nil {
		return nil, err
	}

	return &SMTPMailer{client: client, sender: sender}, nil
}

// Send renders templateFile with data and delivers it to recipient,
// retrying up to three times, half a second apart, before giving up.
func (m *SMTPMailer) Send(recipient, templateFile string, data any) error {
	rendered, err := Render(templateFile, data)
	if err != nil {
		return err
	}

	msg := mail.NewMsg()
	if err := msg.To(recipient); err != nil {
		return err
	}
	if err := msg.From(m.sender); err != nil {
		return err
	}
	msg.Subject(rendered.Subject)
	msg.SetBodyString(mail.TypeTextPlain, rendered.PlainBody)
	msg.AddAlternativeString(mail.TypeTextHTML, rendered.HTMLBody)

	for attempt := 1; attempt <= 3; attempt++ {
		err = m.client.DialAndSend(msg)
		if err == nil {
			return nil
		}
		if attempt < 3 {
			time.Sleep(500 * time.Millisecond)
		}
	}
	return err
}

// LogMailer renders emails and writes them to the log instead of sending
// them. It is only for local development without an SMTP server: the log
// then contains activation and reset tokens, so it must never be used in
// staging or production.
type LogMailer struct {
	Logger *slog.Logger
}

// Send renders templateFile with data and logs the result.
func (m LogMailer) Send(recipient, templateFile string, data any) error {
	rendered, err := Render(templateFile, data)
	if err != nil {
		return err
	}

	m.Logger.Info("email (not sent: no SMTP server configured)",
		"to", recipient,
		"subject", rendered.Subject,
		"body", rendered.PlainBody,
	)
	return nil
}
