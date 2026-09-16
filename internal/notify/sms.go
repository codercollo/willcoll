package notify

import (
	"bytes"
	"context"
	"embed"
	"fmt"
	"text/template"

	"github.com/google/uuid"
)

//go:embed templates/*.tmpl
var templatesFS embed.FS

// SendTemplate renders an SMS template with the Organization's BrandName and
// enqueues it for background delivery.
func (s *Service) SendTemplate(ctx context.Context, orgID uuid.UUID, msisdn, templateFile string, vars map[string]any) error {
	brandVars, err := s.branding.BrandVars(ctx, orgID)
	if err != nil {
		return err
	}

	if vars == nil {
		vars = map[string]any{}
	}
	vars["BrandName"] = brandVars.BrandName

	tmpl, err := template.ParseFS(templatesFS, "templates/"+templateFile)
	if err != nil {
		return fmt.Errorf("parse sms template %s: %w", templateFile, err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, vars); err != nil {
		return fmt.Errorf("render sms template %s: %w", templateFile, err)
	}

	s.dispatch.Enqueue(ctx, msisdn, buf.String())
	return nil
}
