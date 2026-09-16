package domain

import (
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Tenant is the renter (spec §1, §3.2). It is deliberately never called
// anything that could collide with the SaaS "Organization" tenancy boundary.
type Tenant struct {
	id             uuid.UUID
	organizationID uuid.UUID
	fullName       string
	phone          string
	idNumber       string
	createdAt      time.Time
}

// NewTenantInput carries everything needed to construct a Tenant.
type NewTenantInput struct {
	OrganizationID uuid.UUID
	FullName       string
	Phone          string
	IDNumber       string
}

// NewTenant validates the Tenant invariants and returns the entity.
func NewTenant(input NewTenantInput) (*Tenant, error) {
	switch {
	case strings.TrimSpace(input.FullName) == "":
		return nil, errors.New("tenant full name is required")
	case strings.TrimSpace(input.Phone) == "":
		return nil, errors.New("tenant phone is required")
	}

	return &Tenant{
		organizationID: input.OrganizationID,
		fullName:       strings.TrimSpace(input.FullName),
		phone:          strings.TrimSpace(input.Phone),
		idNumber:       strings.TrimSpace(input.IDNumber),
	}, nil
}

func (t *Tenant) ID() uuid.UUID             { return t.id }
func (t *Tenant) OrganizationID() uuid.UUID { return t.organizationID }
func (t *Tenant) FullName() string          { return t.fullName }
func (t *Tenant) Phone() string             { return t.phone }
func (t *Tenant) IDNumber() string          { return t.idNumber }
func (t *Tenant) CreatedAt() time.Time      { return t.createdAt }
