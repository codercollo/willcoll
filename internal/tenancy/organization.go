package tenancy

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Organization is the tenancy boundary itself (spec §1a). It is returned by the
// tenancy service and deliberately has no RLS policy, because it carries no
// organization_id column — it is the boundary every other table is scoped to.
type Organization struct {
	ID               uuid.UUID
	Name             string
	BrandName        string
	SubscriptionTier string
	BillingStatus    string
	OwnerUserID      *uuid.UUID
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// User is the tenancy service's view of a system user. It never exposes the
// password hash.
type User struct {
	ID             uuid.UUID
	OrganizationID uuid.UUID
	FullName       string
	Phone          string
	Email          string
	Role           string
	IsSuperManager bool
	Activated      bool
	Status         string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// CreateOrganizationInput is everything needed to create an Organization and
// its first (super-)Manager in one transaction.
type CreateOrganizationInput struct {
	Name             string
	BrandName        string // optional; defaults to Name at insert time
	SubscriptionTier string // optional; defaults to 'starter'
	BillingStatus    string // optional; defaults to 'active'
	FullName         string
	Phone            string
	Email            string
	PasswordHash     string // bcrypt; the caller hashes before calling
}

var (
	ErrOrganizationNameRequired = errors.New("organization name is required")
	ErrManagerNameRequired      = errors.New("manager full name is required")
	ErrManagerPhoneRequired     = errors.New("manager phone is required")
	ErrManagerEmailRequired     = errors.New("manager email is required")
	ErrManagerPasswordRequired  = errors.New("manager password hash is required")
)

// CreateOrganizationWithFirstManagerTx atomically creates a new Organization,
// its first Manager (activated, active, password already set per spec §3.1a),
// and then points the Organization's owner_user_id at that Manager.
func (s *Service) CreateOrganizationWithFirstManagerTx(ctx context.Context, input CreateOrganizationInput) (Organization, User, error) {
	switch {
	case strings.TrimSpace(input.Name) == "":
		return Organization{}, User{}, ErrOrganizationNameRequired
	case strings.TrimSpace(input.FullName) == "":
		return Organization{}, User{}, ErrManagerNameRequired
	case strings.TrimSpace(input.Phone) == "":
		return Organization{}, User{}, ErrManagerPhoneRequired
	case strings.TrimSpace(input.Email) == "":
		return Organization{}, User{}, ErrManagerEmailRequired
	case input.PasswordHash == "":
		return Organization{}, User{}, ErrManagerPasswordRequired
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Organization{}, User{}, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	org := Organization{
		ID:        uuid.New(),
		Name:      input.Name,
		BrandName: strings.TrimSpace(input.BrandName),
	}
	if org.BrandName == "" {
		org.BrandName = input.Name
	}
	org.SubscriptionTier = strings.TrimSpace(input.SubscriptionTier)
	if org.SubscriptionTier == "" {
		org.SubscriptionTier = "starter"
	}
	org.BillingStatus = strings.TrimSpace(input.BillingStatus)
	if org.BillingStatus == "" {
		org.BillingStatus = "active"
	}

	err = tx.QueryRow(ctx, `
		INSERT INTO organizations (id, name, brand_name, subscription_tier, billing_status)
		VALUES ($1, $2, $3, $4::subscription_tier, $5::org_billing_status)
		RETURNING id, name, brand_name, subscription_tier::text, billing_status::text, created_at, updated_at`,
		org.ID, org.Name, org.BrandName, org.SubscriptionTier, org.BillingStatus,
	).Scan(&org.ID, &org.Name, &org.BrandName, &org.SubscriptionTier, &org.BillingStatus, &org.CreatedAt, &org.UpdatedAt)
	if err != nil {
		return Organization{}, User{}, fmt.Errorf("insert organization: %w", err)
	}

	// The users table is RLS-protected, so scope this transaction to the new
	// Organization before inserting its first user.
	if err := s.Scope(ctx, tx, org.ID); err != nil {
		return Organization{}, User{}, fmt.Errorf("scope transaction: %w", err)
	}

	user := User{
		ID:             uuid.New(),
		OrganizationID: org.ID,
		FullName:       strings.TrimSpace(input.FullName),
		Phone:          strings.TrimSpace(input.Phone),
		Email:          strings.TrimSpace(input.Email),
		Role:           "manager",
		IsSuperManager: true,
		Activated:      true,
		Status:         "active",
	}

	err = tx.QueryRow(ctx, `
		INSERT INTO users (id, organization_id, full_name, phone, email, password_hash, role, is_super_manager, activated, status)
		VALUES ($1, $2, $3, $4, $5, $6, 'manager', true, true, 'active')
		RETURNING id, organization_id, full_name, phone, email, role::text, is_super_manager, activated, status::text, created_at, updated_at`,
		user.ID, user.OrganizationID, user.FullName, user.Phone, user.Email, input.PasswordHash,
	).Scan(&user.ID, &user.OrganizationID, &user.FullName, &user.Phone, &user.Email, &user.Role, &user.IsSuperManager, &user.Activated, &user.Status, &user.CreatedAt, &user.UpdatedAt)
	if err != nil {
		return Organization{}, User{}, fmt.Errorf("insert first manager: %w", err)
	}

	// Now that the user exists, point the Organization at it.
	err = tx.QueryRow(ctx, `
		UPDATE organizations
		SET owner_user_id = $1, updated_at = now()
		WHERE id = $2
		RETURNING updated_at`,
		user.ID, org.ID,
	).Scan(&org.UpdatedAt)
	if err != nil {
		return Organization{}, User{}, fmt.Errorf("set organization owner: %w", err)
	}
	org.OwnerUserID = &user.ID

	if err := tx.Commit(ctx); err != nil {
		return Organization{}, User{}, fmt.Errorf("commit transaction: %w", err)
	}

	return org, user, nil
}
