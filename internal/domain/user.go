package domain

import (
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Role is one of the three system roles (spec §2.1). It is a small value type
// so an invalid role string can never be constructed.
type Role struct {
	name string
}

// NewRole returns a Role for name, or an error if name is not a system role.
func NewRole(name string) (Role, error) {
	switch name {
	case "landlord", "manager", "agent":
		return Role{name: name}, nil
	default:
		return Role{}, errors.New("role must be landlord, manager, or agent")
	}
}

func (r Role) Name() string       { return r.name }
func (r Role) IsLandlord() bool   { return r.name == "landlord" }
func (r Role) IsManager() bool    { return r.name == "manager" }
func (r Role) IsAgent() bool      { return r.name == "agent" }

// User is a system user (spec §2, §3.1). It never carries a password hash —
// credentials are the auth package's concern, not the RBAC identity's.
type User struct {
	id             uuid.UUID
	organizationID uuid.UUID
	fullName       string
	phone          string
	email          string
	role           Role
	isSuperManager bool
	activated      bool
	status         string
	invitedBy      *uuid.UUID
	createdAt      time.Time
	updatedAt      time.Time
}

// NewUserInput carries everything needed to construct a User.
type NewUserInput struct {
	OrganizationID uuid.UUID
	FullName       string
	Phone          string
	Email          string
	Role           string
	IsSuperManager bool
	Activated      bool
	Status         string
	InvitedBy      *uuid.UUID
}

// NewUser validates the User invariants and returns the entity.
func NewUser(input NewUserInput) (*User, error) {
	role, err := NewRole(input.Role)
	if err != nil {
		return nil, err
	}

	status := input.Status
	if status == "" {
		status = "invited"
	}

	switch {
	case strings.TrimSpace(input.FullName) == "":
		return nil, errors.New("user full name is required")
	case strings.TrimSpace(input.Phone) == "":
		return nil, errors.New("user phone is required")
	case strings.TrimSpace(input.Email) == "":
		return nil, errors.New("user email is required")
	case status != "invited" && status != "active" && status != "suspended":
		return nil, errors.New("user status must be invited, active, or suspended")
	}

	return &User{
		organizationID: input.OrganizationID,
		fullName:       strings.TrimSpace(input.FullName),
		phone:          strings.TrimSpace(input.Phone),
		email:          strings.TrimSpace(input.Email),
		role:           role,
		isSuperManager: input.IsSuperManager,
		activated:      input.Activated,
		status:         status,
		invitedBy:      input.InvitedBy,
	}, nil
}

func (u *User) ID() uuid.UUID             { return u.id }
func (u *User) OrganizationID() uuid.UUID { return u.organizationID }
func (u *User) FullName() string          { return u.fullName }
func (u *User) Phone() string             { return u.phone }
func (u *User) Email() string             { return u.email }
func (u *User) Role() Role                { return u.role }
func (u *User) IsSuperManager() bool      { return u.isSuperManager }
func (u *User) Activated() bool           { return u.activated }
func (u *User) Status() string            { return u.status }
func (u *User) InvitedBy() *uuid.UUID     { return u.invitedBy }
func (u *User) CreatedAt() time.Time      { return u.createdAt }
func (u *User) UpdatedAt() time.Time      { return u.updatedAt }

// Agent permission names (spec §2.2) — the PBAC bitset bits.
const (
	PermissionRecordPayments       = "can_record_payments"
	PermissionEditLeases           = "can_edit_leases"
	PermissionEditUnitPricing      = "can_edit_unit_pricing"
	PermissionVoidPayments         = "can_void_payments"
	PermissionViewFinancialReports = "can_view_financial_reports"
	PermissionManageMeterReadings  = "can_manage_meter_readings"
)

// AgentPropertyGrant is one Agent's permission bitset scoped to one property
// (spec §2.2).
type AgentPropertyGrant struct {
	id                     uuid.UUID
	agentID                uuid.UUID
	propertyID             uuid.UUID
	canRecordPayments      bool
	canEditLeases          bool
	canEditUnitPricing     bool
	canVoidPayments        bool
	canViewFinancialReports bool
	canManageMeterReadings bool
	grantedBy              uuid.UUID
	grantedAt              time.Time
	revokedAt              *time.Time
}

// NewAgentPropertyGrantInput carries everything needed to construct a grant.
type NewAgentPropertyGrantInput struct {
	AgentID                 uuid.UUID
	PropertyID              uuid.UUID
	CanRecordPayments       bool
	CanEditLeases           bool
	CanEditUnitPricing      bool
	CanVoidPayments         bool
	CanViewFinancialReports bool
	CanManageMeterReadings  bool
	GrantedBy               uuid.UUID
}

// NewAgentPropertyGrant validates the grant invariants and returns the entity.
func NewAgentPropertyGrant(input NewAgentPropertyGrantInput) (*AgentPropertyGrant, error) {
	switch {
	case input.AgentID == uuid.Nil:
		return nil, errors.New("agent id is required")
	case input.PropertyID == uuid.Nil:
		return nil, errors.New("property id is required")
	case input.GrantedBy == uuid.Nil:
		return nil, errors.New("granted_by is required")
	}

	return &AgentPropertyGrant{
		agentID:                input.AgentID,
		propertyID:             input.PropertyID,
		canRecordPayments:      input.CanRecordPayments,
		canEditLeases:          input.CanEditLeases,
		canEditUnitPricing:     input.CanEditUnitPricing,
		canVoidPayments:        input.CanVoidPayments,
		canViewFinancialReports: input.CanViewFinancialReports,
		canManageMeterReadings: input.CanManageMeterReadings,
		grantedBy:              input.GrantedBy,
	}, nil
}

// Allows reports whether the grant carries the given permission bit (spec §2.2).
func (g *AgentPropertyGrant) Allows(action string) bool {
	switch action {
	case PermissionRecordPayments:
		return g.canRecordPayments
	case PermissionEditLeases:
		return g.canEditLeases
	case PermissionEditUnitPricing:
		return g.canEditUnitPricing
	case PermissionVoidPayments:
		return g.canVoidPayments
	case PermissionViewFinancialReports:
		return g.canViewFinancialReports
	case PermissionManageMeterReadings:
		return g.canManageMeterReadings
	default:
		return false
	}
}

// IsActive reports whether the grant has not been revoked.
func (g *AgentPropertyGrant) IsActive() bool { return g.revokedAt == nil }

func (g *AgentPropertyGrant) ID() uuid.UUID         { return g.id }
func (g *AgentPropertyGrant) AgentID() uuid.UUID    { return g.agentID }
func (g *AgentPropertyGrant) PropertyID() uuid.UUID { return g.propertyID }
func (g *AgentPropertyGrant) GrantedBy() uuid.UUID  { return g.grantedBy }
func (g *AgentPropertyGrant) GrantedAt() time.Time  { return g.grantedAt }
func (g *AgentPropertyGrant) RevokedAt() *time.Time { return g.revokedAt }

