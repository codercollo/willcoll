package domain

import (
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// Meter is the physical water meter attached to a Unit.
type Meter struct {
	id          uuid.UUID
	unitID      uuid.UUID
	meterType   string
	meterNumber string
}

// MeterReading is one monthly snapshot of a meter.
type MeterReading struct {
	id              uuid.UUID
	meterID         uuid.UUID
	readingMonth    time.Time
	previousReading decimal.Decimal
	currentReading  decimal.Decimal
	ratePerM3       decimal.Decimal
	recordedBy      uuid.UUID
	recordedAt      time.Time
}

// NewMeterReadingInput carries everything needed to construct a MeterReading.
type NewMeterReadingInput struct {
	MeterID         uuid.UUID
	ReadingMonth    time.Time
	PreviousReading decimal.Decimal
	CurrentReading  decimal.Decimal
	RatePerM3       decimal.Decimal
	RecordedBy      uuid.UUID
}

// NewMeterReading constructs a MeterReading and validates its own fields.
func NewMeterReading(input NewMeterReadingInput) (*MeterReading, error) {
	if input.ReadingMonth.IsZero() {
		return nil, errors.New("reading month is required")
	}
	if input.CurrentReading.LessThan(input.PreviousReading) {
		return nil, errors.New("current reading must be greater than or equal to previous reading")
	}
	if input.RatePerM3.IsNegative() {
		return nil, errors.New("rate per m3 must be non-negative")
	}

	return &MeterReading{
		meterID:         input.MeterID,
		readingMonth:    input.ReadingMonth,
		previousReading: input.PreviousReading,
		currentReading:  input.CurrentReading,
		ratePerM3:       input.RatePerM3,
		recordedBy:      input.RecordedBy,
	}, nil
}

// UnitsConsumed returns the whole cubic meters consumed this month.
func (r *MeterReading) UnitsConsumed() int {
	return int(r.currentReading.Sub(r.previousReading).IntPart())
}

// Validate enforces the meter-reading monotonicity rule: the current reading
// must never go backwards, either against this reading's previous value or
// against the prior month's reading when one is supplied.
func (r *MeterReading) Validate(previous *MeterReading) error {
	if r.currentReading.LessThan(r.previousReading) {
		return errors.New("current reading must be greater than or equal to previous reading")
	}
	if previous != nil && r.currentReading.LessThan(previous.currentReading) {
		return errors.New("current reading must be greater than or equal to the previous month's reading")
	}
	return nil
}

func (r *MeterReading) ID() uuid.UUID                    { return r.id }
func (r *MeterReading) MeterID() uuid.UUID               { return r.meterID }
func (r *MeterReading) ReadingMonth() time.Time          { return r.readingMonth }
func (r *MeterReading) PreviousReading() decimal.Decimal { return r.previousReading }
func (r *MeterReading) CurrentReading() decimal.Decimal  { return r.currentReading }
func (r *MeterReading) RatePerM3() decimal.Decimal       { return r.ratePerM3 }
func (r *MeterReading) RecordedBy() uuid.UUID            { return r.recordedBy }
func (r *MeterReading) RecordedAt() time.Time            { return r.recordedAt }
