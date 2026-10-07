package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"

	"github.com/codercollo/willcoll/backend/internal/shared/validator"
)

// TokenPlaintextLength is the length of every token GenerateTokenPlaintext
// returns: a 16-byte random value, base32-encoded with no padding.
const TokenPlaintextLength = 26

// GenerateTokenPlaintext returns a random, URL-safe plaintext token: a
// 16-byte random value, base32-encoded (26 characters). Only its hash
// (HashToken) should ever be persisted.
func GenerateTokenPlaintext() (string, error) {
	randomBytes := make([]byte, 16)
	if _, err := rand.Read(randomBytes); err != nil {
		return "", err
	}
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(randomBytes), nil
}

// HashToken hashes a plaintext token the same way every time, so a
// plaintext supplied later (e.g. in an activation request) can be looked
// up by its hash.
func HashToken(plaintext string) []byte {
	hash := sha256.Sum256([]byte(plaintext))
	return hash[:]
}

// ValidateTokenPlaintext checks a client-supplied plaintext token is well formed.
func ValidateTokenPlaintext(v *validator.Validator, tokenPlaintext string) {
	v.Check(tokenPlaintext != "", "token", "must be provided")
	v.Check(len(tokenPlaintext) == TokenPlaintextLength, "token", "must be 26 bytes long")
}
