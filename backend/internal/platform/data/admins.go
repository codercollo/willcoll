package data

import (
	"context"
	"errors"
	"time"

	"github.com/codercollo/willcoll/backend/internal/platform/db"
	"github.com/codercollo/willcoll/backend/internal/platform/db/sqlc"
	"github.com/codercollo/willcoll/backend/internal/shared/validator"
	"github.com/google/uuid"
)

// Admin is the Super Admin (system-design.txt A.1): owns the platform,
// sits outside every tenant, and never touches a manager's tenant data.
type Admin struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	Email     string    `json:"email"`
	Password  password  `json:"-"`
	CreatedAt time.Time `json:"created_at"`
}

// ValidateAdminCredentials checks the configured Super Admin login. The bar
// is higher than for managers: this account can suspend any firm.
func ValidateAdminCredentials(v *validator.Validator, name, email, password string) {
	v.Check(name != "", "ADMIN_NAME", "must be provided")
	v.Check(validator.Matches(email, validator.EmailRX), "ADMIN_EMAIL", "must be a valid email address")
	v.Check(len(password) >= 12, "ADMIN_PASSWORD", "must be at least 12 bytes long")
	v.Check(len(password) <= 72, "ADMIN_PASSWORD", "must not be more than 72 bytes long")
}

// AdminModel is the service layer for the Super Admin's own account. The
// admins table has no RLS; these queries run on the platform app pool.
type AdminModel struct {
	Store db.Store

	// Timeout bounds each method call; zero means DefaultQueryTimeout.
	Timeout time.Duration
}

// Get returns the admin with the given id, or ErrRecordNotFound.
func (m AdminModel) Get(ctx context.Context, id uuid.UUID) (*Admin, error) {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	row, err := m.Store.GetAdmin(ctx, id)
	if err != nil {
		return nil, notFound(err)
	}
	return adminFromRow(row), nil
}

// GetByEmail returns the admin with the given email, or ErrRecordNotFound.
func (m AdminModel) GetByEmail(ctx context.Context, email string) (*Admin, error) {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	row, err := m.Store.GetAdminByEmail(ctx, email)
	if err != nil {
		return nil, notFound(err)
	}
	return adminFromRow(row), nil
}

// Provision makes sure the one Super Admin has this name, email and
// password (the credentials live in .env: ADMIN_NAME / ADMIN_EMAIL /
// ADMIN_PASSWORD). It's a no-op when they already match, so the bcrypt
// hash isn't rewritten on every start. Changing ADMIN_PASSWORD rotates the
// password; changing ADMIN_EMAIL re-points the same admin row (migration
// 000001 allows exactly one). Validate with ValidateAdminCredentials first.
func (m AdminModel) Provision(ctx context.Context, name, email, plaintext string) (*Admin, error) {
	existing, err := m.GetByEmail(ctx, email)
	switch {
	case err == nil:
		if existing.Name == name {
			if ok, err := existing.Password.Matches(plaintext); err == nil && ok {
				return existing, nil
			}
		}
	case !errors.Is(err, ErrRecordNotFound):
		return nil, err
	}

	var pw password
	if err := pw.Set(plaintext); err != nil {
		return nil, err
	}

	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	row, err := m.Store.UpsertAdmin(ctx, sqlc.UpsertAdminParams{Name: name, Email: email, PasswordHash: pw.hash})
	if err != nil {
		return nil, err
	}
	return adminFromRow(row), nil
}

func adminFromRow(row sqlc.Admin) *Admin {
	return &Admin{
		ID:        row.ID,
		Name:      row.Name,
		Email:     row.Email,
		Password:  password{hash: row.PasswordHash},
		CreatedAt: row.CreatedAt,
	}
}
