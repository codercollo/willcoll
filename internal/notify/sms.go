package notify

import (
	"bytes"
	"context"
	"embed"
	"errors"
	"fmt"
	"text/template"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
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

	body, err := s.templateBody(ctx, orgID, templateFile)
	if err != nil {
		return err
	}

	tmpl, err := template.New(templateFile).Parse(body)
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

// templateBody returns the Organization's saved override for templateFile
// (Phase 8.2), or the shipped default when it has never saved one. This is
// the ONLY place SendTemplate's source text comes from — every call site
// keeps sending by the same fixed templateFile name regardless of whether an
// override exists.
func (s *Service) templateBody(ctx context.Context, orgID uuid.UUID, templateFile string) (string, error) {
	if s.pool != nil {
		override, ok, err := s.lookupOverride(ctx, orgID, templateFile)
		if err != nil {
			return "", err
		}
		if ok {
			return override, nil
		}
	}

	raw, err := templatesFS.ReadFile("templates/" + templateFile)
	if err != nil {
		return "", fmt.Errorf("read default template %s: %w", templateFile, err)
	}
	return string(raw), nil
}

// lookupOverride reads sms_template_overrides for (orgID, templateFile). It
// runs in its own short-lived transaction with app.current_org_id set on it
// directly (tenancy.Service.Scope's own pattern) — SendTemplate is called
// from many code paths (webhooks, background billing) that don't always run
// inside a request-scoped, already-tenant-scoped transaction, and a bare
// pool query against an RLS-protected table would otherwise fail the
// unset-GUC case rather than just correctly seeing zero rows.
func (s *Service) lookupOverride(ctx context.Context, orgID uuid.UUID, templateFile string) (string, bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", false, fmt.Errorf("begin template override lookup: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, "SELECT set_config('app.current_org_id', $1, true)", orgID.String()); err != nil {
		return "", false, fmt.Errorf("scope template override lookup: %w", err)
	}

	var body string
	err = tx.QueryRow(ctx, `
		SELECT body
		FROM sms_template_overrides
		WHERE organization_id = $1 AND template_key = $2`,
		orgID, templateFile,
	).Scan(&body)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("lookup template override: %w", err)
	}
	return body, true, nil
}
