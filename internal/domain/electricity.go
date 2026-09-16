package domain

import (
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// ElectricityTopup is a tenant self-reported electricity (KPLC token) topup
// (spec §5.4). It is informational only — no ledger_entries reference this
// table, so it never affects the ledger.
type ElectricityTopup struct {
	id             uuid.UUID
	unitID         uuid.UUID
	reportedBy     *uuid.UUID
	amount         decimal.Decimal
	tokenReference string
	reportedAt     time.Time
}

// NewElectricityTopupInput carries everything needed to construct a topup.
type NewElectricityTopupInput struct {
	UnitID         uuid.UUID
	ReportedBy     *uuid.UUID
	Amount         decimal.Decimal
	TokenReference string
}

// NewElectricityTopup validates the topup invariants and returns the entity.
func NewElectricityTopup(input NewElectricityTopupInput) (*ElectricityTopup, error) {
	if input.UnitID == uuid.Nil {
		return nil, errors.New("electricity topup unit id is required")
	}

	return &ElectricityTopup{
		unitID:         input.UnitID,
		reportedBy:     input.ReportedBy,
		amount:         input.Amount,
		tokenReference: input.TokenReference,
	}, nil
}

func (e *ElectricityTopup) ID() uuid.UUID           { return e.id }
func (e *ElectricityTopup) UnitID() uuid.UUID       { return e.unitID }
func (e *ElectricityTopup) ReportedBy() *uuid.UUID  { return e.reportedBy }
func (e *ElectricityTopup) Amount() decimal.Decimal { return e.amount }
func (e *ElectricityTopup) TokenReference() string  { return e.tokenReference }
func (e *ElectricityTopup) ReportedAt() time.Time   { return e.reportedAt }
