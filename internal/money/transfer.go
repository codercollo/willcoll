package money

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// DepositTxInput carries a lease-deposit money movement.
type DepositTxInput struct {
	OrganizationID uuid.UUID
	TenantID       uuid.UUID
	UnitID         uuid.UUID
	PropertyID     uuid.UUID
	InvoiceID      *uuid.UUID
	Amount         int64 // KES cents
	Method         string
	Reference      string
	Narrative      string
	IdempotencyKey string
	RecordedBy     uuid.UUID
}

// PaymentTxInput carries a rent or water/garbage payment.
type PaymentTxInput struct {
	OrganizationID uuid.UUID
	TenantID       uuid.UUID
	PropertyID     uuid.UUID
	InvoiceID      uuid.UUID
	Amount         int64 // KES cents
	Method         string
	Reference      string
	Narrative      string
	IdempotencyKey string
	RecordedBy     uuid.UUID
}

// LateFeeTxInput carries a late-fee charge to a tenant.
type LateFeeTxInput struct {
	OrganizationID uuid.UUID
	TenantID       uuid.UUID
	PropertyID     uuid.UUID
	InvoiceID      *uuid.UUID
	Amount         int64 // KES cents
	IdempotencyKey string
	RecordedBy     uuid.UUID
}

// RemittanceTxInput carries a remittance from property till to landlord payable.
type RemittanceTxInput struct {
	OrganizationID uuid.UUID
	PropertyID     uuid.UUID
	LandlordID     uuid.UUID
	Amount         int64 // KES cents
	Method         string
	Reference      string
	Narrative      string
	IdempotencyKey string
	RecordedBy     uuid.UUID
}

// DepositRefundTxInput carries a deposit refund on move-out.
type DepositRefundTxInput struct {
	OrganizationID uuid.UUID
	TenantID       uuid.UUID
	UnitID         uuid.UUID
	PropertyID     uuid.UUID
	Amount         int64 // KES cents
	Method         string
	Reference      string
	Narrative      string
	IdempotencyKey string
	RecordedBy     uuid.UUID
}

// ReverseTxInput carries a reversal of a prior transfer.
type ReverseTxInput struct {
	OrganizationID uuid.UUID
	TransferID     uuid.UUID
	IdempotencyKey string
	RecordedBy     uuid.UUID
}

type entryLeg struct {
	ownerType string
	ownerID   uuid.UUID
	amount    int64 // KES cents; positive = credit, negative = debit
}

type transferSpec struct {
	organizationID     uuid.UUID
	transferType       string
	invoiceID          *uuid.UUID
	method             string
	reference          string
	narrative          string
	idempotencyKey     string
	recordedBy         uuid.UUID
	reversedTransferID *uuid.UUID
	legs               []entryLeg
	updateInvoice      bool
	invoiceAmount      int64
}

func kesString(cents int64) string {
	sign := ""
	if cents < 0 {
		sign = "-"
		cents = -cents
	}
	return fmt.Sprintf("%s%d.%02d", sign, cents/100, cents%100)
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// accountFor returns the ledger account id for owner_type/owner_id, creating
// it with a zero balance on first use.
func (s *Service) accountFor(ctx context.Context, tx pgx.Tx, organizationID uuid.UUID, ownerType string, ownerID uuid.UUID) (uuid.UUID, error) {
	var id uuid.UUID
	err := tx.QueryRow(ctx, `
		SELECT id
		FROM ledger_accounts
		WHERE organization_id = $1
		  AND owner_type = $2::account_owner_kind
		  AND owner_id = $3
		  AND currency = 'KES'`,
		organizationID, ownerType, ownerID,
	).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, fmt.Errorf("get account: %w", err)
	}

	id = uuid.New()
	if _, err := tx.Exec(ctx, `
		INSERT INTO ledger_accounts (id, organization_id, owner_type, owner_id, currency)
		VALUES ($1, $2, $3::account_owner_kind, $4, 'KES')
		ON CONFLICT (owner_type, owner_id, currency) DO NOTHING`,
		id, organizationID, ownerType, ownerID,
	); err != nil {
		return uuid.Nil, fmt.Errorf("create account: %w", err)
	}

	err = tx.QueryRow(ctx, `
		SELECT id
		FROM ledger_accounts
		WHERE organization_id = $1
		  AND owner_type = $2::account_owner_kind
		  AND owner_id = $3
		  AND currency = 'KES'`,
		organizationID, ownerType, ownerID,
	).Scan(&id)
	if err != nil {
		return uuid.Nil, fmt.Errorf("reload account: %w", err)
	}
	return id, nil
}

// transferByKey returns an existing transfer with the given idempotency key.
func (s *Service) transferByKey(ctx context.Context, tx pgx.Tx, key string) (Transfer, bool, error) {
	var t Transfer
	err := tx.QueryRow(ctx, `
		SELECT id, organization_id, transfer_type::text, invoice_id, method::text,
		       reference, narrative, idempotency_key, reversed_transfer_id,
		       recorded_by, created_at
		FROM ledger_transfers
		WHERE idempotency_key = $1`,
		key,
	).Scan(
		&t.ID, &t.OrganizationID, &t.TransferType, &t.InvoiceID, &t.Method,
		&t.Reference, &t.Narrative, &t.IdempotencyKey, &t.ReversedTransferID,
		&t.RecordedBy, &t.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Transfer{}, false, nil
	}
	if err != nil {
		return Transfer{}, false, fmt.Errorf("check idempotency: %w", err)
	}
	return t, true, nil
}

func (s *Service) executeTransfer(ctx context.Context, spec transferSpec) (Transfer, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Transfer{}, fmt.Errorf("begin transfer: %w", err)
	}
	defer tx.Rollback(ctx)

	if existing, found, err := s.transferByKey(ctx, tx, spec.idempotencyKey); err != nil {
		return Transfer{}, err
	} else if found {
		return existing, nil
	}

	accountIDs := make([]uuid.UUID, 0, len(spec.legs))
	for _, leg := range spec.legs {
		accountID, err := s.accountFor(ctx, tx, spec.organizationID, leg.ownerType, leg.ownerID)
		if err != nil {
			return Transfer{}, err
		}
		accountIDs = append(accountIDs, accountID)
	}

	if err := s.lockAccounts(ctx, tx, accountIDs); err != nil {
		return Transfer{}, err
	}

	transferID := uuid.New()
	if _, err := tx.Exec(ctx, `
		INSERT INTO ledger_transfers (
			id, organization_id, transfer_type, invoice_id, method, reference,
			narrative, idempotency_key, reversed_transfer_id, recorded_by
		)
		VALUES ($1, $2, $3::transfer_kind, $4, $5::payment_method, $6, $7, $8, $9, $10)`,
		transferID, spec.organizationID, spec.transferType, spec.invoiceID,
		spec.method, spec.reference, spec.narrative, spec.idempotencyKey,
		spec.reversedTransferID, spec.recordedBy,
	); err != nil {
		if isUniqueViolation(err) {
			if existing, found, lookupErr := s.transferByKey(ctx, tx, spec.idempotencyKey); lookupErr == nil && found {
				return existing, nil
			}
		}
		return Transfer{}, fmt.Errorf("insert transfer: %w", err)
	}

	for i, leg := range spec.legs {
		if _, err := tx.Exec(ctx, `
			INSERT INTO ledger_entries (account_id, amount, transfer_id)
			VALUES ($1, $2::numeric, $3)`,
			accountIDs[i], kesString(leg.amount), transferID,
		); err != nil {
			return Transfer{}, fmt.Errorf("insert entry: %w", err)
		}

		if _, err := tx.Exec(ctx, `
			UPDATE ledger_accounts
			SET balance = balance + $2::numeric
			WHERE id = $1`,
			accountIDs[i], kesString(leg.amount),
		); err != nil {
			return Transfer{}, fmt.Errorf("update account balance: %w", err)
		}
	}

	if spec.updateInvoice && spec.invoiceID != nil {
		if _, err := tx.Exec(ctx, `
			UPDATE invoices
			SET amount_paid = amount_paid + $2::numeric,
			    status = CASE
			        WHEN amount_paid + $2::numeric >= amount_due THEN 'paid'::invoice_status
			        WHEN amount_paid + $2::numeric > 0 THEN 'partially_paid'::invoice_status
			        ELSE status
			    END
			WHERE id = $1`,
			*spec.invoiceID, kesString(spec.invoiceAmount),
		); err != nil {
			return Transfer{}, fmt.Errorf("update invoice: %w", err)
		}
	}

	var created Transfer
	err = tx.QueryRow(ctx, `
		SELECT id, organization_id, transfer_type::text, invoice_id, method::text,
		       reference, narrative, idempotency_key, reversed_transfer_id,
		       recorded_by, created_at
		FROM ledger_transfers
		WHERE id = $1`,
		transferID,
	).Scan(
		&created.ID, &created.OrganizationID, &created.TransferType, &created.InvoiceID,
		&created.Method, &created.Reference, &created.Narrative, &created.IdempotencyKey,
		&created.ReversedTransferID, &created.RecordedBy, &created.CreatedAt,
	)
	if err != nil {
		return Transfer{}, fmt.Errorf("reload transfer: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return Transfer{}, fmt.Errorf("commit transfer: %w", err)
	}

	return created, nil
}

// ExecuteDepositTx posts a lease deposit: debit tenant, credit deposit_holding.
func (s *Service) ExecuteDepositTx(ctx context.Context, input DepositTxInput) (Transfer, error) {
	return s.executeTransfer(ctx, transferSpec{
		organizationID: input.OrganizationID,
		transferType:   "deposit_payment",
		invoiceID:      input.InvoiceID,
		method:         input.Method,
		reference:      input.Reference,
		narrative:      input.Narrative,
		idempotencyKey: input.IdempotencyKey,
		recordedBy:     input.RecordedBy,
		legs: []entryLeg{
			{ownerType: "tenant", ownerID: input.TenantID, amount: -input.Amount},
			{ownerType: "deposit_holding", ownerID: input.UnitID, amount: input.Amount},
		},
	})
}

// ExecuteRentPaymentTx posts a rent payment: debit tenant, credit property till,
// and updates the matching rent invoice's read-model fields.
func (s *Service) ExecuteRentPaymentTx(ctx context.Context, input PaymentTxInput) (Transfer, error) {
	return s.executeTransfer(ctx, transferSpec{
		organizationID: input.OrganizationID,
		transferType:   "rent_payment",
		invoiceID:      &input.InvoiceID,
		method:         input.Method,
		reference:      input.Reference,
		narrative:      input.Narrative,
		idempotencyKey: input.IdempotencyKey,
		recordedBy:     input.RecordedBy,
		updateInvoice:  true,
		invoiceAmount:  input.Amount,
		legs: []entryLeg{
			{ownerType: "tenant", ownerID: input.TenantID, amount: -input.Amount},
			{ownerType: "property_till", ownerID: input.PropertyID, amount: input.Amount},
		},
	})
}

// ExecuteWaterGarbageTx posts a water/garbage payment against the matching invoice.
func (s *Service) ExecuteWaterGarbageTx(ctx context.Context, input PaymentTxInput) (Transfer, error) {
	return s.executeTransfer(ctx, transferSpec{
		organizationID: input.OrganizationID,
		transferType:   "water_payment",
		invoiceID:      &input.InvoiceID,
		method:         input.Method,
		reference:      input.Reference,
		narrative:      input.Narrative,
		idempotencyKey: input.IdempotencyKey,
		recordedBy:     input.RecordedBy,
		updateInvoice:  true,
		invoiceAmount:  input.Amount,
		legs: []entryLeg{
			{ownerType: "tenant", ownerID: input.TenantID, amount: -input.Amount},
			{ownerType: "property_till", ownerID: input.PropertyID, amount: input.Amount},
		},
	})
}

// ExecuteLateFeeChargeTx increases what a tenant owes. The property_till leg is
// the balancing debit until a dedicated revenue/income account is introduced.
func (s *Service) ExecuteLateFeeChargeTx(ctx context.Context, input LateFeeTxInput) (Transfer, error) {
	return s.executeTransfer(ctx, transferSpec{
		organizationID: input.OrganizationID,
		transferType:   "late_fee_charge",
		invoiceID:      input.InvoiceID,
		method:         "cash",
		idempotencyKey: input.IdempotencyKey,
		recordedBy:     input.RecordedBy,
		legs: []entryLeg{
			{ownerType: "tenant", ownerID: input.TenantID, amount: input.Amount},
			{ownerType: "property_till", ownerID: input.PropertyID, amount: -input.Amount},
		},
	})
}

// ExecuteRemittanceTx debits property till and credits landlord payable.
func (s *Service) ExecuteRemittanceTx(ctx context.Context, input RemittanceTxInput) (Transfer, error) {
	return s.executeTransfer(ctx, transferSpec{
		organizationID: input.OrganizationID,
		transferType:   "remittance_to_landlord",
		method:         input.Method,
		reference:      input.Reference,
		narrative:      input.Narrative,
		idempotencyKey: input.IdempotencyKey,
		recordedBy:     input.RecordedBy,
		legs: []entryLeg{
			{ownerType: "property_till", ownerID: input.PropertyID, amount: -input.Amount},
			{ownerType: "landlord_payable", ownerID: input.LandlordID, amount: input.Amount},
		},
	})
}

// ExecuteDepositRefundTx debits deposit holding and credits the tenant on move-out.
func (s *Service) ExecuteDepositRefundTx(ctx context.Context, input DepositRefundTxInput) (Transfer, error) {
	return s.executeTransfer(ctx, transferSpec{
		organizationID: input.OrganizationID,
		transferType:   "deposit_refund",
		method:         input.Method,
		reference:      input.Reference,
		narrative:      input.Narrative,
		idempotencyKey: input.IdempotencyKey,
		recordedBy:     input.RecordedBy,
		legs: []entryLeg{
			{ownerType: "deposit_holding", ownerID: input.UnitID, amount: -input.Amount},
			{ownerType: "tenant", ownerID: input.TenantID, amount: input.Amount},
		},
	})
}

// ReverseTransferTx posts the mirror-image entries of a prior transfer, tagged
// as a reversal and linked via reversed_transfer_id.
func (s *Service) ReverseTransferTx(ctx context.Context, input ReverseTxInput) (Transfer, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Transfer{}, fmt.Errorf("begin reversal: %w", err)
	}
	defer tx.Rollback(ctx)

	if existing, found, err := s.transferByKey(ctx, tx, input.IdempotencyKey); err != nil {
		return Transfer{}, err
	} else if found {
		return existing, nil
	}

	var original Transfer
	err = tx.QueryRow(ctx, `
		SELECT id, organization_id, transfer_type::text, invoice_id, method::text,
		       reference, narrative, idempotency_key, reversed_transfer_id,
		       recorded_by, created_at
		FROM ledger_transfers
		WHERE id = $1 AND organization_id = $2`,
		input.TransferID, input.OrganizationID,
	).Scan(
		&original.ID, &original.OrganizationID, &original.TransferType, &original.InvoiceID,
		&original.Method, &original.Reference, &original.Narrative, &original.IdempotencyKey,
		&original.ReversedTransferID, &original.RecordedBy, &original.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Transfer{}, fmt.Errorf("transfer not found")
	}
	if err != nil {
		return Transfer{}, fmt.Errorf("lookup transfer: %w", err)
	}

	rows, err := tx.Query(ctx, `
		SELECT account_id, (amount * 100)::bigint
		FROM ledger_entries
		WHERE transfer_id = $1`,
		input.TransferID,
	)
	if err != nil {
		return Transfer{}, fmt.Errorf("lookup entries: %w", err)
	}
	defer rows.Close()

	type reverseLeg struct {
		accountID uuid.UUID
		amount    int64
	}
	var legs []reverseLeg
	accountIDs := make([]uuid.UUID, 0)
	for rows.Next() {
		var leg reverseLeg
		if err := rows.Scan(&leg.accountID, &leg.amount); err != nil {
			return Transfer{}, fmt.Errorf("scan entry: %w", err)
		}
		leg.amount = -leg.amount
		legs = append(legs, leg)
		accountIDs = append(accountIDs, leg.accountID)
	}
	if err := rows.Err(); err != nil {
		return Transfer{}, fmt.Errorf("iterate entries: %w", err)
	}
	if len(legs) == 0 {
		return Transfer{}, fmt.Errorf("cannot reverse a transfer with no entries")
	}

	if err := s.lockAccounts(ctx, tx, accountIDs); err != nil {
		return Transfer{}, err
	}

	reversalID := uuid.New()
	if _, err := tx.Exec(ctx, `
		INSERT INTO ledger_transfers (
			id, organization_id, transfer_type, invoice_id, method, reference,
			narrative, idempotency_key, reversed_transfer_id, recorded_by
		)
		VALUES ($1, $2, 'reversal'::transfer_kind, $3, $4::payment_method, $5, $6, $7, $8, $9)`,
		reversalID, input.OrganizationID, original.InvoiceID, original.Method,
		original.Reference, original.Narrative, input.IdempotencyKey, original.ID, input.RecordedBy,
	); err != nil {
		if isUniqueViolation(err) {
			if existing, found, lookupErr := s.transferByKey(ctx, tx, input.IdempotencyKey); lookupErr == nil && found {
				return existing, nil
			}
		}
		return Transfer{}, fmt.Errorf("insert reversal: %w", err)
	}

	for _, leg := range legs {
		if _, err := tx.Exec(ctx, `
			INSERT INTO ledger_entries (account_id, amount, transfer_id)
			VALUES ($1, $2::numeric, $3)`,
			leg.accountID, kesString(leg.amount), reversalID,
		); err != nil {
			return Transfer{}, fmt.Errorf("insert reversal entry: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			UPDATE ledger_accounts
			SET balance = balance + $2::numeric
			WHERE id = $1`,
			leg.accountID, kesString(leg.amount),
		); err != nil {
			return Transfer{}, fmt.Errorf("update reversed balance: %w", err)
		}
	}

	var created Transfer
	err = tx.QueryRow(ctx, `
		SELECT id, organization_id, transfer_type::text, invoice_id, method::text,
		       reference, narrative, idempotency_key, reversed_transfer_id,
		       recorded_by, created_at
		FROM ledger_transfers
		WHERE id = $1`,
		reversalID,
	).Scan(
		&created.ID, &created.OrganizationID, &created.TransferType, &created.InvoiceID,
		&created.Method, &created.Reference, &created.Narrative, &created.IdempotencyKey,
		&created.ReversedTransferID, &created.RecordedBy, &created.CreatedAt,
	)
	if err != nil {
		return Transfer{}, fmt.Errorf("reload reversal: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return Transfer{}, fmt.Errorf("commit reversal: %w", err)
	}

	return created, nil
}
