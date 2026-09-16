package auth

import (
	"github.com/google/uuid"
	"github.com/o1egl/paseto"
)

// Claims is the authenticated identity carried inside a v2.local token. It is
// attached to the request context by the auth middleware (spec §2).
type Claims struct {
	UserID         uuid.UUID `json:"user_id"`
	Role           string    `json:"role"`
	OrganizationID uuid.UUID `json:"organization_id"`
}

// IssueToken produces a v2.local PASETO token carrying exactly the user_id,
// role, and organization_id claims (spec §2).
func (s *Service) IssueToken(userID uuid.UUID, role string, organizationID uuid.UUID) (string, error) {
	claims := Claims{
		UserID:         userID,
		Role:           role,
		OrganizationID: organizationID,
	}

	return paseto.NewV2().Encrypt(s.key, claims, []byte{})
}

// VerifyToken parses and authenticates a v2.local PASETO token and returns the
// claims it carries.
func (s *Service) VerifyToken(token string) (Claims, error) {
	var claims Claims
	if err := paseto.NewV2().Decrypt(token, s.key, &claims, nil); err != nil {
		return Claims{}, err
	}

	return claims, nil
}
