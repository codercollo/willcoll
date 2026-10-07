package data

import (
	"errors"
	"sync"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

// password holds a plaintext/hash pair, mirroring
// internal/rentals/data's password type (kept separate, not shared,
// since internal/platform must not import internal/rentals —
// project-structure.txt Part D). Set() hashes the plaintext; Matches()
// verifies a login attempt against the stored hash without exposing it.
type password struct {
	plaintext *string
	hash      []byte
}

// Set hashes plaintextPassword (bcrypt, cost 12) and keeps the plaintext
// only for the duration of the request, so it can be re-validated.
func (p *password) Set(plaintextPassword string) error {
	hash, err := bcrypt.GenerateFromPassword([]byte(plaintextPassword), 12)
	if err != nil {
		return err
	}

	p.plaintext = &plaintextPassword
	p.hash = hash
	return nil
}

// Matches reports whether plaintextPassword hashes to the stored hash.
func (p *password) Matches(plaintextPassword string) (bool, error) {
	err := bcrypt.CompareHashAndPassword(p.hash, []byte(plaintextPassword))
	if err != nil {
		switch {
		case errors.Is(err, bcrypt.ErrMismatchedHashAndPassword):
			return false, nil
		default:
			return false, err
		}
	}
	return true, nil
}

// dummyPasswordHash is a bcrypt hash (cost 12) of a random value, built on
// first use.
var dummyPasswordHash = sync.OnceValue(func() []byte {
	hash, err := bcrypt.GenerateFromPassword([]byte(uuid.NewString()), 12)
	if err != nil {
		panic(err)
	}
	return hash
})

// SpendPasswordCheck does the work of one password comparison against
// nothing. Login handlers call it when the email is unknown, so a failed
// login takes about as long whether or not the account exists, and
// response times can't reveal which emails are registered.
func SpendPasswordCheck(plaintext string) {
	_ = bcrypt.CompareHashAndPassword(dummyPasswordHash(), []byte(plaintext))
}
