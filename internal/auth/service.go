package auth

import "github.com/jackc/pgx/v5/pgxpool"

// Service is the authentication package's entry point. It holds the symmetric
// PASETO key used to issue and verify v2.local tokens (spec §2), plus the DB
// pool for the user lookups login and password reset need.
type Service struct {
	key  []byte
	pool *pgxpool.Pool
}

// NewService constructs an auth Service using key as the v2.local PASETO
// symmetric key. The key must be 32 bytes; IssueToken and VerifyToken surface
// the underlying error if it is not.
func NewService(key []byte, pool *pgxpool.Pool) *Service {
	return &Service{key: key, pool: pool}
}
