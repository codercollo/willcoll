package domain

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

// Property is the named building/portfolio entry a Manager runs day-to-day
// (spec §3.2). It owns the rate-card defaults for every Unit under it.
type Property struct {
	id               uuid.UUID
	organizationID   uuid.UUID
	managerID        uuid.UUID
	name             string
	location         string
	brandNote        string
	waterRatePerM3   float64
	garbageFeeFlat   float64
	lateFeePctPerDay float64
	rentDueDay       int
	createdAt        time.Time
}

// NewPropertyInput carries everything needed to construct a Property.
type NewPropertyInput struct {
	OrganizationID   uuid.UUID
	ManagerID        uuid.UUID
	Name             string
	Location         string
	BrandNote        string
	WaterRatePerM3   float64
	GarbageFeeFlat   float64
	LateFeePctPerDay float64
	RentDueDay       int
}

// NewProperty validates the Property invariants and returns the entity.
func NewProperty(input NewPropertyInput) (*Property, error) {
	switch {
	case input.Name == "":
		return nil, errors.New("property name is required")
	case input.Location == "":
		return nil, errors.New("property location is required")
	case input.RentDueDay < 1 || input.RentDueDay > 31:
		return nil, errors.New("rent due day must be between 1 and 31")
	case input.WaterRatePerM3 < 0:
		return nil, errors.New("water rate per m3 must be non-negative")
	case input.GarbageFeeFlat < 0:
		return nil, errors.New("garbage fee must be non-negative")
	case input.LateFeePctPerDay < 0:
		return nil, errors.New("late fee percentage must be non-negative")
	}

	return &Property{
		organizationID:   input.OrganizationID,
		managerID:        input.ManagerID,
		name:             input.Name,
		location:         input.Location,
		brandNote:        input.BrandNote,
		waterRatePerM3:   input.WaterRatePerM3,
		garbageFeeFlat:   input.GarbageFeeFlat,
		lateFeePctPerDay: input.LateFeePctPerDay,
		rentDueDay:       input.RentDueDay,
	}, nil
}

func (p *Property) ID() uuid.UUID             { return p.id }
func (p *Property) OrganizationID() uuid.UUID { return p.organizationID }
func (p *Property) ManagerID() uuid.UUID      { return p.managerID }
func (p *Property) Name() string              { return p.name }
func (p *Property) Location() string          { return p.location }
func (p *Property) BrandNote() string         { return p.brandNote }
func (p *Property) WaterRatePerM3() float64   { return p.waterRatePerM3 }
func (p *Property) GarbageFeeFlat() float64   { return p.garbageFeeFlat }
func (p *Property) LateFeePctPerDay() float64 { return p.lateFeePctPerDay }
func (p *Property) RentDueDay() int           { return p.rentDueDay }
func (p *Property) CreatedAt() time.Time      { return p.createdAt }
