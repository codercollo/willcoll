package money

import (
	"time"

	"github.com/google/uuid"
)

// Account is one row in ledger_accounts. Balance and amounts are stored in
// integer KES cents to avoid floating-point drift in the ledger.
type Account struct {
	ID             uuid.UUID
	OrganizationID uuid.UUID
	OwnerType      string
	OwnerID        uuid.UUID
	Currency       string
	Balance        int64
	CreatedAt      time.Time
}

// Entry is one immutable row in ledger_entries.
type Entry struct {
	ID         uuid.UUID
	AccountID  uuid.UUID
	Amount     int64 // positive = credit, negative = debit, in KES cents
	TransferID uuid.UUID
	CreatedAt  time.Time
}

// Transfer is the grouping row for one double-entry money movement.
type Transfer struct {
	ID                 uuid.UUID
	OrganizationID     uuid.UUID
	TransferType       string
	InvoiceID          *uuid.UUID
	Method             string
	Reference          string
	Narrative          string
	IdempotencyKey     string
	ReversedTransferID *uuid.UUID
	RecordedBy         uuid.UUID
	CreatedAt          time.Time
}

// IsCredit reports whether this entry increases the account balance.
func (e Entry) IsCredit() bool { return e.Amount > 0 }

// IsDebit reports whether this entry decreases the account balance.
func (e Entry) IsDebit() bool { return e.Amount < 0 }
