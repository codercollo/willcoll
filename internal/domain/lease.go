package domain

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

// Lease is the Neema-House-style agreement snapshot (spec §3.2). It captures
// rent/deposit/late-fee at signing so later Unit changes never rewrite history.
type Lease struct {
	id               uuid.UUID
	unitID           uuid.UUID
	tenantID         uuid.UUID
	monthlyRent      float64
	depositPaid      float64
	startDate        time.Time
	endDate          *time.Time
	rentDueDay       int
	lateFeePctPerDay float64
	status           string
	terminatedReason string
	terminatedAt     *time.Time
	createdBy        uuid.UUID
	createdAt        time.Time
}

// NewLeaseInput carries everything needed to construct a Lease.
type NewLeaseInput struct {
	UnitID           uuid.UUID
	TenantID         uuid.UUID
	MonthlyRent      float64
	DepositPaid      float64
	StartDate        time.Time
	EndDate          *time.Time
	RentDueDay       int
	LateFeePctPerDay float64
	Status           string
	CreatedBy        uuid.UUID
}

// NewLease validates the Lease invariants and returns the entity.
func NewLease(input NewLeaseInput) (*Lease, error) {
	status := input.Status
	if status == "" {
		status = "active"
	}

	switch {
	case input.MonthlyRent < 0:
		return nil, errors.New("monthly rent must be non-negative")
	case input.DepositPaid < 0:
		return nil, errors.New("deposit paid must be non-negative")
	case input.StartDate.IsZero():
		return nil, errors.New("lease start date is required")
	case input.RentDueDay < 1 || input.RentDueDay > 31:
		return nil, errors.New("rent due day must be between 1 and 31")
	case input.LateFeePctPerDay < 0:
		return nil, errors.New("late fee percentage must be non-negative")
	case status != "active" && status != "terminated" && status != "notice_period":
		return nil, errors.New("lease status must be active, terminated, or notice_period")
	}

	return &Lease{
		unitID:           input.UnitID,
		tenantID:         input.TenantID,
		monthlyRent:      input.MonthlyRent,
		depositPaid:      input.DepositPaid,
		startDate:        input.StartDate,
		endDate:          input.EndDate,
		rentDueDay:       input.RentDueDay,
		lateFeePctPerDay: input.LateFeePctPerDay,
		status:           status,
		createdBy:        input.CreatedBy,
	}, nil
}

// IsActive reports whether the lease is currently active.
func (l *Lease) IsActive() bool { return l.status == "active" }

// DaysPastDue returns how many days the rent is overdue as of asOf, based on
// this lease's own rentDueDay. Non-active leases are never past due.
func (l *Lease) DaysPastDue(asOf time.Time) int {
	if !l.IsActive() {
		return 0
	}

	due := dueDateInMonth(asOf, l.rentDueDay)
	if !asOf.After(due) {
		return 0
	}
	return int(asOf.Sub(due).Hours() / 24)
}

func dueDateInMonth(asOf time.Time, day int) time.Time {
	first := time.Date(asOf.Year(), asOf.Month(), 1, 0, 0, 0, 0, asOf.Location())
	lastDay := first.AddDate(0, 1, -1).Day()
	if day > lastDay {
		day = lastDay
	}
	return time.Date(asOf.Year(), asOf.Month(), day, 0, 0, 0, 0, asOf.Location())
}

func (l *Lease) ID() uuid.UUID             { return l.id }
func (l *Lease) UnitID() uuid.UUID         { return l.unitID }
func (l *Lease) TenantID() uuid.UUID       { return l.tenantID }
func (l *Lease) MonthlyRent() float64      { return l.monthlyRent }
func (l *Lease) DepositPaid() float64      { return l.depositPaid }
func (l *Lease) StartDate() time.Time      { return l.startDate }
func (l *Lease) EndDate() *time.Time       { return l.endDate }
func (l *Lease) RentDueDay() int           { return l.rentDueDay }
func (l *Lease) LateFeePctPerDay() float64 { return l.lateFeePctPerDay }
func (l *Lease) Status() string            { return l.status }
func (l *Lease) TerminatedReason() string  { return l.terminatedReason }
func (l *Lease) TerminatedAt() *time.Time  { return l.terminatedAt }
func (l *Lease) CreatedBy() uuid.UUID      { return l.createdBy }
func (l *Lease) CreatedAt() time.Time      { return l.createdAt }
