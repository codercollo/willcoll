package domain

import (
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// Invoice is the read-model of what is owed. amount_paid/status are written
// exclusively by internal/money (spec §4.3).
type Invoice struct {
	id             uuid.UUID
	organizationID uuid.UUID
	leaseID        uuid.UUID
	periodMonth    time.Time
	invoiceType    string
	amountDue      decimal.Decimal
	amountPaid     decimal.Decimal
	status         string
	meterReadingID *uuid.UUID
	createdAt      time.Time
}

// RemainingBalance returns amount_due minus amount_paid.
func (i *Invoice) RemainingBalance() decimal.Decimal {
	return i.amountDue.Sub(i.amountPaid)
}

// IsSettled reports whether the invoice has been paid in full.
func (i *Invoice) IsSettled() bool {
	return i.amountPaid.GreaterThanOrEqual(i.amountDue)
}

func (i *Invoice) ID() uuid.UUID               { return i.id }
func (i *Invoice) OrganizationID() uuid.UUID   { return i.organizationID }
func (i *Invoice) LeaseID() uuid.UUID          { return i.leaseID }
func (i *Invoice) PeriodMonth() time.Time      { return i.periodMonth }
func (i *Invoice) InvoiceType() string         { return i.invoiceType }
func (i *Invoice) AmountDue() decimal.Decimal  { return i.amountDue }
func (i *Invoice) AmountPaid() decimal.Decimal { return i.amountPaid }
func (i *Invoice) Status() string              { return i.status }
func (i *Invoice) MeterReadingID() *uuid.UUID  { return i.meterReadingID }
func (i *Invoice) CreatedAt() time.Time        { return i.createdAt }
