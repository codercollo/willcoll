package branding

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

// BrandVars is the minimal brand context injected into SMS and email
// templates so they never hardcode a platform brand string.
type BrandVars struct {
	BrandName string
	LogoURL   string
}

// BrandVars returns the Organization's brand name and logo URL for template
// rendering. A NULL logo_url becomes an empty string so templates can fall back
// without knowing about SQL NULL.
func (s *Service) BrandVars(ctx context.Context, orgID uuid.UUID) (BrandVars, error) {
	var v BrandVars
	var logoURL *string

	err := s.pool.QueryRow(ctx, `
		SELECT brand_name, logo_url
		FROM organizations
		WHERE id = $1`,
		orgID,
	).Scan(&v.BrandName, &logoURL)
	if err != nil {
		return BrandVars{}, fmt.Errorf("get brand vars: %w", err)
	}
	if logoURL != nil {
		v.LogoURL = *logoURL
	}

	return v, nil
}
