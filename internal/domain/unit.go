package domain

import (
	"errors"

	"github.com/google/uuid"
)

// Unit is the durable "HSE NO." row in the paper ledgers (spec §3.2). Tenants
// come and go; the Unit, its rent, and its meter history persist.
type Unit struct {
	id            uuid.UUID
	propertyID    uuid.UUID
	unitLabel     string
	unitType      string
	baseRent      float64
	depositAmount float64
	status        string
}

// NewUnitInput carries everything needed to construct a Unit.
type NewUnitInput struct {
	PropertyID    uuid.UUID
	UnitLabel     string
	UnitType      string
	BaseRent      float64
	DepositAmount float64
	Status        string
}

// NewUnit validates the Unit invariants and returns the entity.
func NewUnit(input NewUnitInput) (*Unit, error) {
	status := input.Status
	if status == "" {
		status = "vacant"
	}

	switch {
	case input.UnitLabel == "":
		return nil, errors.New("unit label is required")
	case input.BaseRent < 0:
		return nil, errors.New("base rent must be non-negative")
	case input.DepositAmount < 0:
		return nil, errors.New("deposit amount must be non-negative")
	case status != "vacant" && status != "occupied" && status != "notice_given":
		return nil, errors.New("unit status must be vacant, occupied, or notice_given")
	}

	return &Unit{
		propertyID:    input.PropertyID,
		unitLabel:     input.UnitLabel,
		unitType:      input.UnitType,
		baseRent:      input.BaseRent,
		depositAmount: input.DepositAmount,
		status:        status,
	}, nil
}

// IsVacant reports whether the Unit has no active occupant.
func (u *Unit) IsVacant() bool { return u.status == "vacant" }

// IsOccupied reports whether the Unit currently has an occupant.
func (u *Unit) IsOccupied() bool { return u.status == "occupied" }

func (u *Unit) ID() uuid.UUID          { return u.id }
func (u *Unit) PropertyID() uuid.UUID  { return u.propertyID }
func (u *Unit) UnitLabel() string      { return u.unitLabel }
func (u *Unit) UnitType() string       { return u.unitType }
func (u *Unit) BaseRent() float64      { return u.baseRent }
func (u *Unit) DepositAmount() float64 { return u.depositAmount }
func (u *Unit) Status() string         { return u.status }
