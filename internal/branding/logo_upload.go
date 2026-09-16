package branding

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
)

const maxLogoBytes = 512 * 1024

var (
	ErrLogoContentTypeInvalid = errors.New("logo must be a PNG, SVG, or JPEG image")
	ErrLogoTooLarge           = errors.New("logo must be 512KB or smaller")
)

// UploadLogo accepts PNG/SVG/JPEG only, enforces a 512KB cap, writes the file
// to LOGO_STORAGE_PATH/{organization_id}.{ext}, and stores the resulting
// /logos/... URL in organizations.logo_url.
func (s *Service) UploadLogo(ctx context.Context, orgID uuid.UUID, file io.Reader, contentType string) (string, error) {
	ext, err := extensionForContentType(contentType)
	if err != nil {
		return "", err
	}

	data, err := io.ReadAll(io.LimitReader(file, maxLogoBytes+1))
	if err != nil {
		return "", fmt.Errorf("read logo: %w", err)
	}
	if len(data) > maxLogoBytes {
		return "", ErrLogoTooLarge
	}
	if len(data) == 0 {
		return "", errors.New("logo file is empty")
	}

	if err := os.MkdirAll(s.logoStoragePath, 0o755); err != nil {
		return "", fmt.Errorf("create logo directory: %w", err)
	}

	filename := orgID.String() + "." + ext
	filePath := filepath.Join(s.logoStoragePath, filename)
	if err := os.WriteFile(filePath, data, 0o644); err != nil {
		return "", fmt.Errorf("write logo: %w", err)
	}

	logoURL := "/logos/" + filename
	if err := s.updateLogoURL(ctx, orgID, logoURL); err != nil {
		return "", err
	}

	return logoURL, nil
}

func extensionForContentType(contentType string) (string, error) {
	contentType = strings.TrimSpace(strings.SplitN(contentType, ";", 2)[0])
	switch contentType {
	case "image/png":
		return "png", nil
	case "image/jpeg":
		return "jpg", nil
	case "image/svg+xml":
		return "svg", nil
	default:
		return "", ErrLogoContentTypeInvalid
	}
}

func (s *Service) updateLogoURL(ctx context.Context, orgID uuid.UUID, logoURL string) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE organizations
		SET logo_url = $2, updated_at = now()
		WHERE id = $1`,
		orgID, logoURL,
	)
	if err != nil {
		return fmt.Errorf("update logo url: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("organization %s not found", orgID)
	}
	return nil
}
