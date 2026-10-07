package data

import (
	"context"
	"time"

	"github.com/codercollo/willcoll/backend/internal/platform/db"
	"github.com/codercollo/willcoll/backend/internal/platform/db/sqlc"
	"github.com/codercollo/willcoll/backend/internal/shared/auth"
	"github.com/codercollo/willcoll/backend/internal/shared/validator"
	"github.com/google/uuid"
)

// Token scopes (system-design.txt A.1).
const (
	ScopeActivation    = "activation"
	ScopePasswordReset = "password-reset"
)

// Token is a manager's or a staff member's activation/password-reset
// token: a random value emailed once in plaintext, stored only as its
// SHA-256 hash (internal/shared/auth's scheme). Belongs to exactly one of
// ManagerID/StaffID (migration 000004's exactly-one-owner check).
type Token struct {
	Plaintext string     `json:"token"`
	Hash      []byte     `json:"-"`
	ManagerID *uuid.UUID `json:"-"`
	StaffID   *uuid.UUID `json:"-"`
	Expiry    time.Time  `json:"expiry"`
	Scope     string     `json:"-"`
}

func generateToken(ttl time.Duration, scope string) (*Token, error) {
	plaintext, err := auth.GenerateTokenPlaintext()
	if err != nil {
		return nil, err
	}

	return &Token{
		Plaintext: plaintext,
		Hash:      auth.HashToken(plaintext),
		Expiry:    time.Now().Add(ttl),
		Scope:     scope,
	}, nil
}

// ValidateTokenPlaintext checks a client-supplied plaintext token is well formed.
func ValidateTokenPlaintext(v *validator.Validator, tokenPlaintext string) {
	auth.ValidateTokenPlaintext(v, tokenPlaintext)
}

// TokenModel is the service layer for tokens. The tokens table has no RLS,
// so its queries run on the Store directly.
type TokenModel struct {
	Store db.Store

	// Timeout bounds each method call; zero means DefaultQueryTimeout.
	Timeout time.Duration
}

// New generates a Token for managerID and persists it.
func (m TokenModel) New(ctx context.Context, managerID uuid.UUID, ttl time.Duration, scope string) (*Token, error) {
	token, err := generateToken(ttl, scope)
	if err != nil {
		return nil, err
	}
	token.ManagerID = &managerID

	err = m.Insert(ctx, token)
	return token, err
}

// NewForStaff generates a Token for staffID (invite/password-reset) and
// persists it.
func (m TokenModel) NewForStaff(ctx context.Context, staffID uuid.UUID, ttl time.Duration, scope string) (*Token, error) {
	token, err := generateToken(ttl, scope)
	if err != nil {
		return nil, err
	}
	token.StaffID = &staffID

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
		StaffID:   token.StaffID,
		Expiry:    token.Expiry,
		Scope:     token.Scope,
	})
}

// DeleteEveryScopeForManager removes every token managerID holds, of any
// scope: after a password reset, no outstanding emailed link should work.
func (m TokenModel) DeleteEveryScopeForManager(ctx context.Context, managerID uuid.UUID) error {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	return m.Store.DeleteTokensForManager(ctx, &managerID)
}

// DeleteAllForManager removes every token of scope belonging to managerID,
// so a used or superseded token can never be replayed.
func (m TokenModel) DeleteAllForManager(ctx context.Context, scope string, managerID uuid.UUID) error {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	return m.Store.DeleteAllTokensForManager(ctx, sqlc.DeleteAllTokensForManagerParams{
		Scope:     scope,
		ManagerID: &managerID,
	})
}

// DeleteAllForStaff removes every token of scope belonging to staffID.
func (m TokenModel) DeleteAllForStaff(ctx context.Context, scope string, staffID uuid.UUID) error {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	return m.Store.DeleteAllTokensForStaff(ctx, sqlc.DeleteAllTokensForStaffParams{
		Scope:   scope,
		StaffID: &staffID,
	})
}
