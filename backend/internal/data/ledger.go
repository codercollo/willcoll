package data

import (
	"context"
	"errors"
	"time"

	"github.com/codercollo/willcoll/backend/internal/db"
	"github.com/codercollo/willcoll/backend/internal/db/sqlc"
	"github.com/codercollo/willcoll/backend/pkg/moneyfmt"
	"github.com/google/uuid"
)

// Ledger account types (system-design.txt 3.2/3.3). Every unit gets an
// anchor ledger_accounts row per type — never a shared "balance" column.
const (
	LedgerTypeRent               = "RENT"
	LedgerTypeWater              = "WATER"
	LedgerTypeGarbage            = "GARBAGE"
	LedgerTypeRentDeposit        = "RENT_DEPOSIT"
	LedgerTypeWaterDeposit       = "WATER_DEPOSIT"
	LedgerTypeElectricityDeposit = "ELECTRICITY_DEPOSIT"
)

const (
	DirectionDebit  = "DEBIT"
	DirectionCredit = "CREDIT"
)

// Transaction header types (system-design.txt 3.4).
const (
	TxTypeRentRun          = "RENT_RUN"
	TxTypeWaterRun         = "WATER_RUN"
	TxTypeGarbageRun       = "GARBAGE_RUN"
	TxTypeLeaseStart       = "LEASE_START"
	TxTypePaymentPosting   = "PAYMENT_POSTING"
	TxTypeManualAdjustment = "MANUAL_ADJUSTMENT"
	TxTypeReversal         = "REVERSAL"
	TxTypeOpeningBalance   = "OPENING_BALANCE"
)

// Ledger entry reference types.
const (
	ReferenceTypeCharge            = "charge"
	ReferenceTypeOpeningBalance    = "opening_balance"
	ReferenceTypePaymentAllocation = "payment_allocation"
	ReferenceTypeReversal          = "reversal"
)

// Charge source types (system-design.txt 3.3).
const (
	ChargeSourceRentRun      = "rent_run"
	ChargeSourceWaterReading = "water_reading"
	ChargeSourceGarbageRun   = "garbage_run"
	ChargeSourceLeaseStart   = "lease_start"
	ChargeSourceManual       = "manual"
	ChargeSourceOpening      = "opening_balance"
)

// Balance is one unit's derived balance for one ledger type, read from the
// unit_ledger_balances view — never a stored column.
type Balance struct {
	UnitID uuid.UUID      `json:"unit_id"`
	Type   string         `json:"type"`
	Amount moneyfmt.Money `json:"balance"`
}

// LedgerEntry is one row of the immutable ledger_entries table.
type LedgerEntry struct {
	ID                  uuid.UUID      `json:"id"`
	TenantID            uuid.UUID      `json:"-"`
	TransactionHeaderID uuid.UUID      `json:"transaction_header_id"`
	LedgerAccountID     uuid.UUID      `json:"ledger_account_id"`
	Direction           string         `json:"direction"`
	Amount              moneyfmt.Money `json:"amount"`
	ReferenceType       string         `json:"reference_type"`
	ReferenceID         uuid.UUID      `json:"reference_id"`
	CreatedAt           time.Time      `json:"created_at"`

	// Display fields, filled when an entry is read for a ledger view.
	// RunningBalance is the account balance after this entry; Reversed is
	// true once a later reversal has cancelled it; Description is the
	// transaction description (for a reversal, its reason).
	TransactionType string         `json:"transaction_type,omitempty"`
	Description     string         `json:"description,omitempty"`
	RunningBalance  moneyfmt.Money `json:"running_balance"`
	Reversed        bool           `json:"reversed"`
}

// TransactionHeader groups a set of ledger entries atomically
// (system-design.txt 3.4).
type TransactionHeader struct {
	Type           string
	IdempotencyKey string
	Description    string
	CreatedBy      string
}

// Charge is WHAT IS OWED.
type Charge struct {
	SourceType  string
	SourceID    *uuid.UUID
	Period      time.Time
	Amount      moneyfmt.Money
	Description string
	CreatedBy   uuid.UUID
}

// LedgerModel is the service layer over the append-only ledger (ADR 0002).
//
// By design it has no Update or Delete method, and nothing else in this
// package has one for charges, transaction_headers, ledger_entries or
// payment_allocations: a posted entry is never edited, only reversed with
// a storno. There is no version column to conflict on because there is
// nothing to update (Greenlight ch.8.2 divergence). TestAppendOnly enforces
// this in code; migrations 000018/000019 enforce it in the database.
type LedgerModel struct {
	Store db.Store

	// Timeout bounds each method call; zero means DefaultQueryTimeout.
	Timeout time.Duration
}

// UnitLedger is one unit's ledger for one type: its balance and a page of
// entries, newest first.
type UnitLedger struct {
	Balance  Balance
	Entries  []*LedgerEntry
	Metadata Metadata
}

// GetUnitLedger returns the balance and a page of entries for one of the
// tenant's units, or ErrRecordNotFound if the unit isn't theirs.
func (m LedgerModel) GetUnitLedger(ctx context.Context, tenantID, unitID uuid.UUID, ledgerType string, filters Filters) (*UnitLedger, error) {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	var ledger UnitLedger

	err := m.Store.ExecTenantTx(ctx, tenantID, func(q sqlc.Querier) error {
		if _, err := q.GetUnit(ctx, sqlc.GetUnitParams{TenantID: tenantID, ID: unitID}); err != nil {
			return notFound(err)
		}

		balance, err := q.GetUnitLedgerBalance(ctx, sqlc.GetUnitLedgerBalanceParams{
			TenantID: tenantID, UnitID: unitID, LedgerType: ledgerType,
		})
		if err != nil {
			return err
		}

		rows, err := q.ListLedgerEntriesWithBalance(ctx, sqlc.ListLedgerEntriesWithBalanceParams{
			TenantID:   tenantID,
			UnitID:     unitID,
			LedgerType: ledgerType,
			Sort:       filters.Sort,
			PageLimit:  int32(filters.limit()),
			PageOffset: int32(filters.offset()),
		})
		if err != nil {
			return err
		}

		// Rebuilt on every attempt: ExecTenantTx may re-run this closure.
		totalRecords := 0
		entries := make([]*LedgerEntry, 0, len(rows))
		for _, row := range rows {
			totalRecords = int(row.TotalRecords)
			entries = append(entries, &LedgerEntry{
				ID: row.ID, TenantID: tenantID, TransactionHeaderID: row.TransactionHeaderID,
				LedgerAccountID: row.LedgerAccountID, Direction: row.Direction, Amount: row.Amount,
				ReferenceType: row.ReferenceType, ReferenceID: row.ReferenceID, CreatedAt: row.CreatedAt,
				TransactionType: row.TransactionType, Description: row.Description,
				RunningBalance: row.RunningBalance, Reversed: row.Reversed,
			})
		}

		ledger = UnitLedger{
			Balance:  Balance{UnitID: unitID, Type: ledgerType, Amount: balance},
			Entries:  entries,
			Metadata: calculateMetadata(totalRecords, filters.Page, filters.PageSize),
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	return &ledger, nil
}

// debitCharge is one owed amount to post: the charge row plus the DEBIT
// entry that puts it on the unit's ledger account of LedgerType.
type debitCharge struct {
	UnitID     uuid.UUID
	LedgerType string
	Charge     Charge
}

// postCharges writes charges and their DEBIT entries under one
// transaction_header, inside the caller's tenant transaction q. It is the
// single posting path for lease-start deposits and (later) rent, water and
// garbage runs, and it only ever INSERTs.
func postCharges(ctx context.Context, q sqlc.Querier, tenantID uuid.UUID, header TransactionHeader, charges []debitCharge) error {
	if len(charges) == 0 {
		return errors.New("ledger: cannot post a transaction with zero entries")
	}

	headerID, err := q.CreateTransactionHeader(ctx, sqlc.CreateTransactionHeaderParams{
		TenantID:       tenantID,
		Type:           header.Type,
		IdempotencyKey: header.IdempotencyKey,
		Description:    header.Description,
		CreatedBy:      header.CreatedBy,
	})
	if err != nil {
		return err
	}

	for _, dc := range charges {
		accountID, err := q.GetOrCreateLedgerAccount(ctx, sqlc.GetOrCreateLedgerAccountParams{
			TenantID: tenantID, UnitID: dc.UnitID, Type: dc.LedgerType,
		})
		if err != nil {
			return err
		}

		chargeID, err := q.CreateCharge(ctx, sqlc.CreateChargeParams{
			TenantID:        tenantID,
			LedgerAccountID: accountID,
			SourceType:      dc.Charge.SourceType,
			SourceID:        dc.Charge.SourceID,
			Period:          dc.Charge.Period,
			Amount:          dc.Charge.Amount,
			Description:     dc.Charge.Description,
			CreatedBy:       dc.Charge.CreatedBy,
		})
		if err != nil {
			return err
		}

		_, err = q.CreateLedgerEntry(ctx, sqlc.CreateLedgerEntryParams{
			TenantID:            tenantID,
			TransactionHeaderID: headerID,
			LedgerAccountID:     accountID,
			Direction:           DirectionDebit,
			Amount:              dc.Charge.Amount,
			ReferenceType:       ReferenceTypeCharge,
			ReferenceID:         chargeID,
		})
		if err != nil {
			return err
		}
	}

	return nil
}
