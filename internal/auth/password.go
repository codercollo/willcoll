package auth

import "golang.org/x/crypto/bcrypt"

// HashPassword returns a bcrypt hash of plaintext using bcrypt.DefaultCost.
func (s *Service) HashPassword(plaintext string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(plaintext), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}

	return string(hash), nil
}

// ComparePassword reports whether plaintext matches the bcrypt hash.
func (s *Service) ComparePassword(hash, plaintext string) error {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(plaintext))
}
