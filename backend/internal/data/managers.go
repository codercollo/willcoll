package data

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/codercollo/willcoll/backend/internal/db"
	"github.com/codercollo/willcoll/backend/internal/db/sqlc"
	"github.com/codercollo/willcoll/backend/internal/validator"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"golang.org/x/crypto/bcrypt"
)

// Manager statuses (system-design.txt 3.1).
const (
	ManagerStatusPending   = "pending"
	ManagerStatusActive    = "active"
	ManagerStatusSuspended = "suspended"
)

var (
	// ErrDuplicateEmail is returned when a manager's email is already registered.
	ErrDuplicateEmail = errors.New("duplicate email")

	// ErrDuplicateUsername is returned when a manager's username is taken.
	ErrDuplicateUsername = errors.New("duplicate username")
)

// password holds a plaintext/hash pair, mirroring Greenlight's password
// type: Set() hashes the plaintext, Matches() verifies a login attempt
// against the stored hash without ever exposing it.
type password struct {
	plaintext *string
	hash      []byte
}

// Set hashes plaintextPassword (bcrypt, cost 12 per system-design.txt 4.4)
// and keeps the plaintext only for the duration of the request, so it can
// be validated.
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

// Manager is the SaaS tenant (a management firm) — system-design.txt 3.1.
type Manager struct {
	ID          uuid.UUID  `json:"id"`
	FirmName    string     `json:"firm_name"`
	Username    string     `json:"username"`
	Email       string     `json:"email"`
	Phone       string     `json:"phone"`
	Password    password   `json:"-"`
	Status      string     `json:"status"`
	ActivatedAt *time.Time `json:"activated_at,omitempty"`
	// TrialEndsAt is when the free trial ends; nil until the firm activates.
	TrialEndsAt *time.Time `json:"trial_ends_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	Version     int32      `json:"-"`
}

// AnonymousManager represents an unauthenticated request — Greenlight's
// AnonymousUser sentinel, so handlers can check manager.IsAnonymous()
// instead of comparing against nil.
var AnonymousManager = &Manager{}

// IsAnonymous reports whether m is the anonymous sentinel.
func (m *Manager) IsAnonymous() bool {
	return m == AnonymousManager
}

func ValidateManagerEmail(v *validator.Validator, email string) {
	v.Check(email != "", "email", "must be provided")
	v.Check(validator.Matches(email, validator.EmailRX), "email", "must be a valid email address")
}

func ValidateManagerPasswordPlaintext(v *validator.Validator, password string) {
	v.Check(password != "", "password", "must be provided")
	v.Check(len(password) >= 8, "password", "must be at least 8 bytes long")
	v.Check(len(password) <= 72, "password", "must not be more than 72 bytes long")
}

func ValidateManager(v *validator.Validator, manager *Manager) {
	v.Check(manager.FirmName != "", "firm_name", "must be provided")
	v.Check(len(manager.FirmName) <= 500, "firm_name", "must not be more than 500 bytes long")

	v.Check(manager.Username != "", "username", "must be provided")
	v.Check(len(manager.Username) <= 100, "username", "must not be more than 100 bytes long")

	v.Check(manager.Phone != "", "phone", "must be provided")
	v.Check(validator.Matches(manager.Phone, validator.PhoneRX), "phone", "must be a valid E.164 phone number")

	ValidateManagerEmail(v, manager.Email)

	if manager.Password.plaintext != nil {
		ValidateManagerPasswordPlaintext(v, *manager.Password.plaintext)
	}

	if manager.Password.hash == nil {
		panic("missing password hash for manager")
	}
}

// ManagerModel is the service layer for managers. The managers table sits
// outside the tenant boundary (no RLS), so its queries run on the Store
// directly rather than inside ExecTenantTx.
type ManagerModel struct {
	Store db.Store

	// Timeout bounds each method call; zero means DefaultQueryTimeout.
	Timeout time.Duration
}

// Insert creates manager, filling in ID, CreatedAt and Version. Returns
// ErrDuplicateEmail or ErrDuplicateUsername.
func (m ManagerModel) Insert(ctx context.Context, manager *Manager) error {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	row, err := m.Store.CreateManager(ctx, sqlc.CreateManagerParams{
		FirmName:     manager.FirmName,
		Username:     manager.Username,
		Email:        manager.Email,
		Phone:        manager.Phone,
		PasswordHash: manager.Password.hash,
		Status:       manager.Status,
	})
	if err != nil {
		return managerWriteError(err)
	}

	manager.ID = row.ID
	manager.CreatedAt = row.CreatedAt
	manager.Version = row.Version
	return nil
}

// Get returns the manager with the given id, or ErrRecordNotFound.
func (m ManagerModel) Get(ctx context.Context, id uuid.UUID) (*Manager, error) {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	row, err := m.Store.GetManager(ctx, id)
	if err != nil {
		return nil, notFound(err)
	}
	return managerFromRow(row), nil
}

// GetByEmail returns the manager registered with email (case-insensitive:
// the column is citext), or ErrRecordNotFound.
func (m ManagerModel) GetByEmail(ctx context.Context, email string) (*Manager, error) {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	row, err := m.Store.GetManagerByEmail(ctx, email)
	if err != nil {
		return nil, notFound(err)
	}
	return managerFromRow(row), nil
}

// GetForToken returns the manager a still-valid token of tokenScope
// belongs to, or ErrRecordNotFound.
func (m ManagerModel) GetForToken(ctx context.Context, tokenScope, tokenPlaintext string) (*Manager, error) {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	row, err := m.Store.GetManagerForToken(ctx, sqlc.GetManagerForTokenParams{
		Hash:  sha256Sum(tokenPlaintext),
		Scope: tokenScope,
	})
	if err != nil {
		return nil, notFound(err)
	}
	return managerFromRow(row), nil
}

// Update saves manager if its Version still matches the stored row, and
// bumps Version on success. Returns ErrEditConflict, ErrDuplicateEmail or
// ErrDuplicateUsername.
func (m ManagerModel) Update(ctx context.Context, manager *Manager) error {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	version, err := m.Store.UpdateManager(ctx, sqlc.UpdateManagerParams{
		ID:           manager.ID,
		Version:      manager.Version,
		FirmName:     manager.FirmName,
		Username:     manager.Username,
		Email:        manager.Email,
		Phone:        manager.Phone,
		PasswordHash: manager.Password.hash,
		Status:       manager.Status,
		ActivatedAt:  manager.ActivatedAt,
		TrialEndsAt:  manager.TrialEndsAt,
	})
	if err != nil {
		return managerWriteError(editConflict(err))
	}

	manager.Version = version
	return nil
}

func managerWriteError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == pgUniqueViolation {
		switch pgErr.ConstraintName {
		case "managers_email_key":
			return ErrDuplicateEmail
		case "managers_username_key":
			return ErrDuplicateUsername
		}
	}
	return err
}

func managerFromRow(row sqlc.Manager) *Manager {
	return &Manager{
		ID:          row.ID,
		FirmName:    row.FirmName,
		Username:    row.Username,
		Email:       row.Email,
		Phone:       row.Phone,
		Password:    password{hash: row.PasswordHash},
		Status:      row.Status,
		ActivatedAt: row.ActivatedAt,
		TrialEndsAt: row.TrialEndsAt,
		CreatedAt:   row.CreatedAt,
		Version:     row.Version,
	}
}
