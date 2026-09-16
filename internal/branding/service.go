package branding

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// validAccentKeys is the Go-side mirror of the database CHECK constraint added
// in migration 000002. It must stay in sync with design-tokens.txt §7.
var validAccentKeys = [5]string{"orange", "teal", "indigo", "crimson", "forest"}

const brandNameMaxRunes = 60

const defaultLogoStoragePath = "/var/lib/willcoll/logos"

var (
	ErrBrandNameRequired = errors.New("brand name is required")
	ErrBrandNameTooLong  = fmt.Errorf("brand name must be %d characters or fewer", brandNameMaxRunes)
	ErrBrandNameInvalid  = errors.New("brand name must be plain text")
	ErrAccentKeyInvalid  = errors.New("invalid accent key")
)

// Branding is the public white-label view of an Organization (spec §1b.1).
type Branding struct {
	BrandName   string  `json:"brand_name"`
	Slug        string  `json:"slug"`
	LogoURL     *string `json:"logo_url"`
	AccentKey   string  `json:"accent_key"`
	SMSSenderID *string `json:"sms_sender_id"`
}

// UpdateBrandingInput is the set of Organization branding fields a Manager may
// change. Logo uploads are handled separately in Phase 3.3.
type UpdateBrandingInput struct {
	BrandName     string
	AccentKey     string
	SMSSenderID   string
	EmailFromName string
}

// Service is the branding package's entry point. It holds the DB pool and owns
// the only writes to the Organization branding columns.
type Service struct {
	pool            *pgxpool.Pool
	logoStoragePath string
}

// NewService constructs a branding Service backed by pool. Logo uploads are
// written under LOGO_STORAGE_PATH (defaulting to /var/lib/willcoll/logos).
func NewService(pool *pgxpool.Pool) *Service {
	logoStoragePath := os.Getenv("LOGO_STORAGE_PATH")
	if logoStoragePath == "" {
		logoStoragePath = defaultLogoStoragePath
	}
	return &Service{pool: pool, logoStoragePath: logoStoragePath}
}

// GetBranding returns the public branding fields for an Organization.
func (s *Service) GetBranding(ctx context.Context, orgID uuid.UUID) (Branding, error) {
	var b Branding
	err := s.pool.QueryRow(ctx, `
		SELECT brand_name, slug, logo_url, accent_key, sms_sender_id
		FROM organizations
		WHERE id = $1`,
		orgID,
	).Scan(&b.BrandName, &b.Slug, &b.LogoURL, &b.AccentKey, &b.SMSSenderID)
	if err != nil {
		return Branding{}, fmt.Errorf("get branding: %w", err)
	}
	return b, nil
}

// GetBrandingBySlug returns the public branding fields for an Organization
// looked up by its URL slug.
func (s *Service) GetBrandingBySlug(ctx context.Context, slug string) (Branding, error) {
	var b Branding
	err := s.pool.QueryRow(ctx, `
		SELECT brand_name, slug, logo_url, accent_key, sms_sender_id
		FROM organizations
		WHERE slug = $1`,
		slug,
	).Scan(&b.BrandName, &b.Slug, &b.LogoURL, &b.AccentKey, &b.SMSSenderID)
	if err != nil {
		return Branding{}, fmt.Errorf("get branding by slug: %w", err)
	}
	return b, nil
}

// UpdateBranding validates and writes the Organization's editable branding
// fields, then returns the fresh public view.
func (s *Service) UpdateBranding(ctx context.Context, orgID uuid.UUID, input UpdateBrandingInput) (Branding, error) {
	brandName := strings.TrimSpace(input.BrandName)
	if err := validateBrandName(brandName); err != nil {
		return Branding{}, err
	}
	if err := validateAccentKey(input.AccentKey); err != nil {
		return Branding{}, err
	}

	var b Branding
	err := s.pool.QueryRow(ctx, `
		UPDATE organizations
		SET brand_name = $2,
		    accent_key = $3,
		    sms_sender_id = NULLIF($4, ''),
		    email_from_name = NULLIF($5, ''),
		    updated_at = now()
		WHERE id = $1
		RETURNING brand_name, slug, logo_url, accent_key, sms_sender_id`,
		orgID,
		brandName,
		input.AccentKey,
		strings.TrimSpace(input.SMSSenderID),
		strings.TrimSpace(input.EmailFromName),
	).Scan(&b.BrandName, &b.Slug, &b.LogoURL, &b.AccentKey, &b.SMSSenderID)
	if err != nil {
		return Branding{}, fmt.Errorf("update branding: %w", err)
	}
	return b, nil
}

func validateBrandName(name string) error {
	if name == "" {
		return ErrBrandNameRequired
	}
	if utf8.RuneCountInString(name) > brandNameMaxRunes {
		return ErrBrandNameTooLong
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return ErrBrandNameInvalid
		}
	}
	return nil
}

func validateAccentKey(key string) error {
	for _, valid := range validAccentKeys {
		if key == valid {
			return nil
		}
	}
	return ErrAccentKeyInvalid
}
