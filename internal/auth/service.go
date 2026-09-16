package auth

// Service is the authentication package's entry point. It holds the symmetric
// PASETO key used to issue and verify v2.local tokens (spec §2).
type Service struct {
	key []byte
}

// NewService constructs an auth Service using key as the v2.local PASETO
// symmetric key. The key must be 32 bytes; IssueToken and VerifyToken surface
// the underlying error if it is not.
func NewService(key []byte) *Service {
	return &Service{key: key}
}
