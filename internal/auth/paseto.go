package auth

import (
	"crypto/sha256"
	"errors"

	"github.com/google/uuid"
	"github.com/o1egl/paseto"
	"golang.org/x/crypto/hkdf"
)

// Claims is the authenticated identity carried inside a v2.local token. It is
// attached to the request context by the auth middleware (spec §2).
type Claims struct {
	UserID         uuid.UUID `json:"user_id"`
	Role           string    `json:"role"`
	OrganizationID uuid.UUID `json:"organization_id"`
}

// scopedRoles are the three roles that get their own PASETO signing key
// (LGF §16.2-16.3 pattern, substituting PASETO for JWT): a leaked/forged key
// for one scope can't be replayed to mint tokens for another.
var scopedRoles = [...]string{"manager", "agent", "landlord"}

// roleKey derives a 32-byte v2.local key for role via HKDF-SHA256 over the
// service's master key, so one configured secret yields three independent
// per-role keys instead of provisioning and rotating three separately.
func (s *Service) roleKey(role string) []byte {
	key := make([]byte, 32)
	_, _ = hkdf.New(sha256.New, s.key, nil, []byte("willcoll:paseto:"+role)).Read(key)
	return key
}

// IssueToken produces a v2.local PASETO token, encrypted under role's own
// key, carrying exactly the user_id, role, and organization_id claims
// (spec §2).
func (s *Service) IssueToken(userID uuid.UUID, role string, organizationID uuid.UUID) (string, error) {
	claims := Claims{
		UserID:         userID,
		Role:           role,
		OrganizationID: organizationID,
	}

	return paseto.NewV2().Encrypt(s.roleKey(role), claims, []byte{})
}

// VerifyToken authenticates a v2.local PASETO token. The role claim lives
// inside the ciphertext, so which per-role key applies isn't known up front;
// it tries each in turn and only a token issued under that same role's key
// will decrypt.
func (s *Service) VerifyToken(token string) (Claims, error) {
	for _, role := range scopedRoles {
		var claims Claims
		if err := paseto.NewV2().Decrypt(token, s.roleKey(role), &claims, nil); err == nil && claims.Role == role {
			return claims, nil
		}
	}

	return Claims{}, errors.New("invalid or expired token")
}
