package mailer

import (
	"bytes"
	"context"
	"embed"
	"fmt"
	"html/template"
	"net"
	"net/smtp"
)

//go:embed templates/*.tmpl
var templatesFS embed.FS

// Send renders the named template file and delivers it to recipient over SMTP.
func (s *Service) Send(ctx context.Context, recipient, templateFile string, data any) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	tmpl, err := template.ParseFS(templatesFS, "templates/"+templateFile)
	if err != nil {
		return fmt.Errorf("parse template %s: %w", templateFile, err)
	}

	subject, err := renderTemplate(tmpl, "subject", data)
	if err != nil {
		return err
	}
	plainBody, err := renderTemplate(tmpl, "plainBody", data)
	if err != nil {
		return err
	}
	htmlBody, err := renderTemplate(tmpl, "htmlBody", data)
	if err != nil {
		return err
	}

	fromName := ""
	if m, ok := data.(map[string]string); ok {
		fromName = m["EmailFromName"]
		if fromName == "" {
			if brand := m["BrandName"]; brand != "" {
				fromName = brand + " via Willcoll"
			}
		}
	}

	addr := net.JoinHostPort(s.config.Host, s.config.Port)
	auth := smtp.PlainAuth("", s.config.Username, s.config.Password, s.config.Host)
	msg := buildMessage(fromName, s.config.Sender, recipient, subject, plainBody, htmlBody)

	if err := smtp.SendMail(addr, auth, s.config.Sender, []string{recipient}, msg); err != nil {
		return fmt.Errorf("send mail: %w", err)
	}

	return nil
}

func renderTemplate(tmpl *template.Template, name string, data any) (string, error) {
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, name, data); err != nil {
		return "", fmt.Errorf("render template %s: %w", name, err)
	}
	return buf.String(), nil
}

func buildMessage(fromName, fromAddr, to, subject, plainBody, htmlBody string) []byte {
	const boundary = "willcoll-mailer-boundary"

	var buf bytes.Buffer
	fmt.Fprintf(&buf, "MIME-Version: 1.0\r\n")
	if fromName != "" {
		fmt.Fprintf(&buf, "From: <%s> <%s>\r\n", fromName, fromAddr)
	} else {
		fmt.Fprintf(&buf, "From: <%s>\r\n", fromAddr)
	}
	fmt.Fprintf(&buf, "To: <%s>\r\n", to)
	fmt.Fprintf(&buf, "Subject: %s\r\n", subject)
	fmt.Fprintf(&buf, "Content-Type: multipart/alternative; boundary=%q\r\n", boundary)
	fmt.Fprintf(&buf, "\r\n")
	fmt.Fprintf(&buf, "--%s\r\n", boundary)
	fmt.Fprintf(&buf, "Content-Type: text/plain; charset=\"UTF-8\"\r\n")
	fmt.Fprintf(&buf, "\r\n")
	fmt.Fprintf(&buf, "%s\r\n", plainBody)
	fmt.Fprintf(&buf, "--%s\r\n", boundary)
	fmt.Fprintf(&buf, "Content-Type: text/html; charset=\"UTF-8\"\r\n")
	fmt.Fprintf(&buf, "\r\n")
	fmt.Fprintf(&buf, "%s\r\n", htmlBody)
	fmt.Fprintf(&buf, "--%s--\r\n", boundary)

	return buf.Bytes()
}
