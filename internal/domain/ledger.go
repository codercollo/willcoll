package domain

import (
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// Account is one row in ledger_accounts — a double-entry account (spec §4.1).
// The four owner kinds mirror the paper collection reality; balance is a cache
// whose source of truth is SUM(ledger_entries).
type Account struct {
	id             uuid.UUID
	organizationID uuid.UUID
	ownerType      string
	ownerID        uuid.UUID
	currency       string
	balance        decimal.Decimal
	createdAt      time.Time
}

// NewAccountInput carries everything needed to construct an Account.
type NewAccountInput struct {
	OrganizationID uuid.UUID
	OwnerType      string
	OwnerID        uuid.UUID
	Currency       string
}

// NewAccount validates the Account invariants and returns the entity.
func NewAccount(input NewAccountInput) (*Account, error) {
	currency := input.Currency
	if currency == "" {
		currency = "KES"
	}

	switch {
	case input.OwnerID == uuid.Nil:
		return nil, errors.New("account owner id is required")
	case input.OwnerType != "tenant" && input.OwnerType != "property_till" &&
		input.OwnerType != "landlord_payable" && input.OwnerType != "deposit_holding":
		return nil, errors.New("account owner type must be tenant, property_till, landlord_payable, or deposit_holding")
	}

	return &Account{
		organizationID: input.OrganizationID,
		ownerType:      input.OwnerType,
		ownerID:        input.OwnerID,
		currency:       currency,
	}, nil
}

func (a *Account) ID() uuid.UUID             { return a.id }
func (a *Account) OrganizationID() uuid.UUID { return a.organizationID }
func (a *Account) OwnerType() string         { return a.ownerType }
func (a *Account) OwnerID() uuid.UUID        { return a.ownerID }
func (a *Account) Currency() string          { return a.currency }
func (a *Account) Balance() decimal.Decimal  { return a.balance }
func (a *Account) CreatedAt() time.Time      { return a.createdAt }

// Entry is one immutable row in ledger_entries (spec §4.2). A positive amount is
// a credit to the account, a negative amount a debit; entries are never updated
// or deleted — reversals are new entries, never a mutation.
type Entry struct {
	id         uuid.UUID
	accountID  uuid.UUID
	amount     decimal.Decimal
	transferID uuid.UUID
	createdAt  time.Time
}

// NewEntryInput carries everything needed to construct an Entry.
type NewEntryInput struct {
	AccountID  uuid.UUID
	Amount     decimal.Decimal
	TransferID uuid.UUID
}

// NewEntry validates the Entry invariants and returns the entity.
func NewEntry(input NewEntryInput) (*Entry, error) {
	switch {
	case input.AccountID == uuid.Nil:
		return nil, errors.New("entry account id is required")
	case input.TransferID == uuid.Nil:
		return nil, errors.New("entry transfer id is required")
	case input.Amount.IsZero():
		return nil, errors.New("entry amount must be non-zero")
	}

	return &Entry{
		accountID:  input.AccountID,
		amount:     input.Amount,
		transferID: input.TransferID,
	}, nil
}

// IsCredit reports whether the entry increases the account balance.
func (e *Entry) IsCredit() bool { return e.amount.Sign() > 0 }

// IsDebit reports whether the entry decreases the account balance.
func (e *Entry) IsDebit() bool { return e.amount.Sign() < 0 }

func (e *Entry) ID() uuid.UUID           { return e.id }
func (e *Entry) AccountID() uuid.UUID    { return e.accountID }
func (e *Entry) Amount() decimal.Decimal { return e.amount }
func (e *Entry) TransferID() uuid.UUID   { return e.transferID }
func (e *Entry) CreatedAt() time.Time    { return e.createdAt }

// Transfer is the grouping row for one double-entry money movement (spec §4.2).
type Transfer struct {
	id                 uuid.UUID
	organizationID     uuid.UUID
	transferType       string
	invoiceID          *uuid.UUID
	method             string
	reference          string
	narrative          string
	idempotencyKey     string
	reversedTransferID *uuid.UUID
	recordedBy         uuid.UUID
	createdAt          time.Time
}

// NewTransferInput carries everything needed to construct a Transfer.
type NewTransferInput struct {
	OrganizationID     uuid.UUID
	TransferType       string
	InvoiceID          *uuid.UUID
	Method             string
	Reference          string
	Narrative          string
	IdempotencyKey     string
	ReversedTransferID *uuid.UUID
	RecordedBy         uuid.UUID
}

// NewTransfer validates the Transfer invariants and returns the entity.
func NewTransfer(input NewTransferInput) (*Transfer, error) {
	switch {
	case input.IdempotencyKey == "":
		return nil, errors.New("transfer idempotency key is required")
	case input.RecordedBy == uuid.Nil:
		return nil, errors.New("transfer recorded_by is required")
	case !validTransferKind(input.TransferType):
		return nil, errors.New("transfer type is invalid")
	case !validPaymentMethod(input.Method):
		return nil, errors.New("payment method is invalid")
	}

	return &Transfer{
		organizationID:     input.OrganizationID,
		transferType:       input.TransferType,
		invoiceID:          input.InvoiceID,
		method:             input.Method,
		reference:          input.Reference,
		narrative:          input.Narrative,
		idempotencyKey:     input.IdempotencyKey,
		reversedTransferID: input.ReversedTransferID,
		recordedBy:         input.RecordedBy,
	}, nil
}

func validTransferKind(k string) bool {
	switch k {
	case "rent_payment", "water_payment", "deposit_payment", "deposit_refund",
		"remittance_to_landlord", "late_fee_charge", "reversal":
		return true
	}
	return false
}

func validPaymentMethod(m string) bool {
	switch m {
	case "cash", "mpesa_manual", "bank", "cheque", "intasend_mpesa", "intasend_card":
		return true
	}
	return false
}

func (t *Transfer) ID() uuid.UUID                  { return t.id }
func (t *Transfer) OrganizationID() uuid.UUID      { return t.organizationID }
func (t *Transfer) TransferType() string           { return t.transferType }
func (t *Transfer) InvoiceID() *uuid.UUID          { return t.invoiceID }
func (t *Transfer) Method() string                 { return t.method }
func (t *Transfer) Reference() string              { return t.reference }
func (t *Transfer) Narrative() string              { return t.narrative }
func (t *Transfer) IdempotencyKey() string         { return t.idempotencyKey }
func (t *Transfer) ReversedTransferID() *uuid.UUID { return t.reversedTransferID }
func (t *Transfer) RecordedBy() uuid.UUID          { return t.recordedBy }
func (t *Transfer) CreatedAt() time.Time           { return t.createdAt }

