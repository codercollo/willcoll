package auth

import (
	"testing"

	"github.com/google/uuid"
	"github.com/o1egl/paseto"
)

func TestIssueVerifyTokenRoundTrip(t *testing.T) {
	s := NewService([]byte("01234567890123456789012345678901"), nil)
	userID, orgID := uuid.New(), uuid.New()

	token, err := s.IssueToken(userID, "agent", orgID)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	claims, err := s.VerifyToken(token)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if claims.UserID != userID || claims.Role != "agent" || claims.OrganizationID != orgID {
		t.Fatalf("claims = %+v, want user=%s role=agent org=%s", claims, userID, orgID)
	}
}

// TestVerifyTokenRejectsCrossRoleKey proves each role's key is independent:
// tampering a token's role claim without re-encrypting under that role's key
// must fail, since manager/agent/landlord tokens are encrypted with distinct
// derived keys.
func TestVerifyTokenRejectsCrossRoleKey(t *testing.T) {
	s := NewService([]byte("01234567890123456789012345678901"), nil)

	agentToken, err := s.IssueToken(uuid.New(), "agent", uuid.New())
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	// Decrypting under the manager key must not succeed for a token issued
	// under the agent key.
	var claims Claims
	if err := paseto.NewV2().Decrypt(agentToken, s.roleKey("manager"), &claims, nil); err == nil {
		t.Fatal("expected decrypt under the wrong role's key to fail")
	}
}
