package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"time"
)

const (
	// ActivationTokenTTL is how long an invited Agent has to set their first
	// password (spec §3.1a).
	ActivationTokenTTL = 72 * time.Hour

	// PasswordResetTTL is how long a Manager-triggered Agent password-reset
	// token stays valid (spec §3.1a).
	PasswordResetTTL = 45 * time.Minute
)

// Token is a single-use activation or password-reset token. Plaintext is what
// gets emailed; Hash is the SHA-256 value persisted in the tokens table.
type Token struct {
	Plaintext string
	Hash      []byte
	Expiry    time.Time
}

// GenerateActivationToken creates a single-use activation token (3-day expiry).
func (s *Service) GenerateActivationToken() (Token, error) {
	return s.generateToken(ActivationTokenTTL)
}

// GeneratePasswordResetToken creates a single-use password-reset token
// (45-minute expiry).
func (s *Service) GeneratePasswordResetToken() (Token, error) {
	return s.generateToken(PasswordResetTTL)
}

// HashToken returns the SHA-256 hash stored in the tokens table for the given
// plaintext token.
func (s *Service) HashToken(plaintext string) []byte {
	sum := sha256.Sum256([]byte(plaintext))
	return sum[:]
}

// Expired reports whether the token has passed asOf.
func (t Token) Expired(asOf time.Time) bool {
	return !asOf.Before(t.Expiry)
}

func (s *Service) generateToken(ttl time.Duration) (Token, error) {
	raw := make([]byte, 26)
	if _, err := rand.Read(raw); err != nil {
		return Token{}, err
	}

	plaintext := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(raw)
	return Token{
		Plaintext: plaintext,
		Hash:      s.HashToken(plaintext),
		Expiry:    time.Now().Add(ttl),
	}, nil
}
