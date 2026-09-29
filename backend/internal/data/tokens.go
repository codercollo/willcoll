package data

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"time"

	"github.com/codercollo/willcoll/backend/internal/db"
	"github.com/codercollo/willcoll/backend/internal/db/sqlc"
	"github.com/codercollo/willcoll/backend/internal/validator"
	"github.com/google/uuid"
)

// Token scopes (system-design.txt 3.1).
const (
	ScopeActivation    = "activation"
	ScopePasswordReset = "password-reset"
)

// Token is Greenlight's generic activation/password-reset token: a random
// value emailed once in plaintext, stored only as its SHA-256 hash.
type Token struct {
	Plaintext string    `json:"token"`
	Hash      []byte    `json:"-"`
	ManagerID uuid.UUID `json:"-"`
	Expiry    time.Time `json:"expiry"`
	Scope     string    `json:"-"`
}

// generateToken creates a new Token for managerID, valid for ttl, scoped to
// scope. The plaintext is a random 16-byte value, base32-encoded (26
// characters); only its SHA-256 hash is ever persisted.
func generateToken(managerID uuid.UUID, ttl time.Duration, scope string) (*Token, error) {
	token := &Token{
		ManagerID: managerID,
		Expiry:    time.Now().Add(ttl),
		Scope:     scope,
	}

	randomBytes := make([]byte, 16)
	if _, err := rand.Read(randomBytes); err != nil {
		return nil, err
	}

	token.Plaintext = base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(randomBytes)
	token.Hash = sha256Sum(token.Plaintext)

	return token, nil
}

// sha256Sum hashes a plaintext token the same way generateToken does, so a
// plaintext supplied later (e.g. in an activation request) can be looked
// up by its hash.
func sha256Sum(plaintext string) []byte {
	hash := sha256.Sum256([]byte(plaintext))
	return hash[:]
}

// ValidateTokenPlaintext checks a client-supplied plaintext token is well formed.
func ValidateTokenPlaintext(v *validator.Validator, tokenPlaintext string) {
	v.Check(tokenPlaintext != "", "token", "must be provided")
	v.Check(len(tokenPlaintext) == 26, "token", "must be 26 bytes long")
}

// TokenModel is the service layer for tokens. The tokens table sits outside
// the tenant boundary (no RLS), so its queries run on the Store directly.
type TokenModel struct {
	Store db.Store

	// Timeout bounds each method call; zero means DefaultQueryTimeout.
	Timeout time.Duration
}

// New generates a Token for managerID and persists it.
func (m TokenModel) New(ctx context.Context, managerID uuid.UUID, ttl time.Duration, scope string) (*Token, error) {
	token, err := generateToken(managerID, ttl, scope)
	if err != nil {
		return nil, err
	}

	err = m.Insert(ctx, token)
	return token, err
}

// Insert persists a Token's hash.
func (m TokenModel) Insert(ctx context.Context, token *Token) error {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	return m.Store.CreateToken(ctx, sqlc.CreateTokenParams{
		Hash:      token.Hash,
		ManagerID: token.ManagerID,
		Expiry:    token.Expiry,
		Scope:     token.Scope,
	})
}

// DeleteEveryScopeForManager removes every token managerID holds, of any
// scope: after a password reset, no outstanding emailed link should work.
func (m TokenModel) DeleteEveryScopeForManager(ctx context.Context, managerID uuid.UUID) error {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	return m.Store.DeleteTokensForManager(ctx, managerID)
}

// DeleteAllForManager removes every token of scope belonging to managerID,
// so a used or superseded token can never be replayed.
func (m TokenModel) DeleteAllForManager(ctx context.Context, scope string, managerID uuid.UUID) error {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	return m.Store.DeleteAllTokensForManager(ctx, sqlc.DeleteAllTokensForManagerParams{
		Scope:     scope,
		ManagerID: managerID,
	})
}
