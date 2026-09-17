package money

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// ManualPaymentInput carries a Manager-recorded payment a tenant already made
// outside the app (Manual Payment Recording brief). Method/reference/photo
// are metadata only — they never determine what the payment is FOR; purpose
// comes from which invoice(s) AllocateManualPayment applies the amount to,
// driven entirely by each invoice's own invoice_type.
type ManualPaymentInput struct {
	OrganizationID  uuid.UUID
	UnitID          uuid.UUID
	Amount          int64  // KES cents, the raw amount received
	Method          string // "cash" | "mpesa" | "bank" | "card"
	ReferenceNumber string
	ReceiptPhotoURL string
	RecordedBy      uuid.UUID
}

// AllocationLeg is one open invoice a manual payment was applied to.
type AllocationLeg struct {
	InvoiceID   uuid.UUID
	InvoiceType string
	PeriodMonth time.Time
	Applied     int64 // KES cents applied to this invoice
	Transfer    Transfer
}

// AllocationResult is the per-invoice-type breakdown of one manual payment.
type AllocationResult struct {
	UnitID      uuid.UUID
	TotalAmount int64 // KES cents handed in
	Allocations []AllocationLeg

	// UnallocatedAmount is what's left once every open invoice on the unit
	// is fully settled — an overpayment/credit. AllocateManualPayment never
	// posts this to the ledger or drops it silently; it's the caller's job
	// to surface it to the Manager (spec: phase 2.1).
	UnallocatedAmount int64
}

var (
	ErrManualPaymentAmountInvalid = errors.New("amount must be greater than zero")
	ErrManualPaymentMethodInvalid = errors.New("method must be cash, mpesa, bank, or card")
	ErrDuplicateReferenceNumber   = errors.New("this reference number has already been recorded")
)

// legacyMethodFor maps the phase-1 manual_payment_method taxonomy onto the
// pre-existing, NOT NULL payment_method column every ledger_transfers row
// still carries (spec §4.3's original method/reference/recorded_by trio,
// kept unchanged so gateway-collected and other transfer kinds are
// unaffected).
func legacyMethodFor(method string) (string, error) {
	switch method {
	case "cash":
		return "cash", nil
	case "mpesa":
		return "mpesa_manual", nil
	case "bank":
		return "bank", nil
	case "card":
		return "card_manual", nil
	default:
		return "", ErrManualPaymentMethodInvalid
	}
}

type openInvoiceRow struct {
	id          uuid.UUID
	invoiceType string
	periodMonth time.Time
	tenantID    uuid.UUID
	propertyID  uuid.UUID
	remaining   int64 // KES cents
}

// openInvoicesForUnit returns the unit's open (status IN ('open',
// 'partially_paid')) rent and water/garbage invoices, ordered arrears
// (overdue rent, oldest first) → current/future rent → water/garbage
// (oldest period first). Deposit and late_fee invoices are excluded: they
// don't go through this manual-payment allocation.
func (s *Service) openInvoicesForUnit(ctx context.Context, unitID uuid.UUID) ([]openInvoiceRow, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT i.id, i.invoice_type::text, i.period_month, l.tenant_id, u.property_id,
		       ((i.amount_due - i.amount_paid) * 100)::bigint AS remaining_cents
		FROM invoices i
		JOIN leases l ON l.id = i.lease_id
		JOIN units u ON u.id = l.unit_id
		WHERE u.id = $1
		  AND i.status IN ('open', 'partially_paid')
		  AND i.invoice_type IN ('rent', 'water_garbage')
		ORDER BY
		  CASE
		    WHEN i.invoice_type = 'rent' AND i.period_month < date_trunc('month', CURRENT_DATE)::date THEN 0
		    WHEN i.invoice_type = 'rent' THEN 1
		    ELSE 2
		  END,
		  i.period_month ASC`,
		unitID,
	)
	if err != nil {
		return nil, fmt.Errorf("list open invoices: %w", err)
	}
	defer rows.Close()

	var invoices []openInvoiceRow
	for rows.Next() {
		var inv openInvoiceRow
		if err := rows.Scan(&inv.id, &inv.invoiceType, &inv.periodMonth, &inv.tenantID, &inv.propertyID, &inv.remaining); err != nil {
			return nil, fmt.Errorf("scan open invoice: %w", err)
		}
		invoices = append(invoices, inv)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate open invoices: %w", err)
	}

	return invoices, nil
}

// AllocateManualPayment applies one Manager-recorded amount across a unit's
// open invoices — arrears first, then current rent, then water/garbage,
// each invoice kept separate and never merged — posting through the
// existing ExecuteRentPaymentTx/ExecuteWaterGarbageTx per invoice touched,
// so "purpose" is stamped by each invoice's own invoice_type, never typed
// freely by the Manager. Any amount left over once every open invoice is
// fully settled is returned as UnallocatedAmount, not dropped.
func (s *Service) AllocateManualPayment(ctx context.Context, input ManualPaymentInput) (AllocationResult, error) {
	if input.Amount <= 0 {
		return AllocationResult{}, ErrManualPaymentAmountInvalid
	}

	legacyMethod, err := legacyMethodFor(input.Method)
	if err != nil {
		return AllocationResult{}, err
	}

	invoices, err := s.openInvoicesForUnit(ctx, input.UnitID)
	if err != nil {
		return AllocationResult{}, err
	}

	result := AllocationResult{UnitID: input.UnitID, TotalAmount: input.Amount}
	remaining := input.Amount

	// The (organization_id, reference_number) unique index (Phase 1) guards
	// one physical receipt/M-Pesa code against being recorded twice —
	// org-wide, not per invoice. A single manual payment can settle several
	// invoices in one call, which would post several ledger_transfers rows;
	// only the first carries reference_number (and the manual-method/photo
	// metadata that travels with it), so those rows never collide with each
	// other. Every leg still carries the plain-text `reference` field for
	// its own audit trail.
	firstLeg := true

	for _, inv := range invoices {
		if remaining <= 0 {
			break
		}
		if inv.remaining <= 0 {
			continue
		}

		applied := inv.remaining
		if applied > remaining {
			applied = remaining
		}

		// Fresh per leg, deliberately NOT derived from ReferenceNumber: the
		// reference_number unique index is the one and only duplicate guard
		// here (phase 2.2). Deriving this from the reference number instead
		// would make a second, logically distinct payment that happens to
		// reuse an old reference AND land on an invoice already touched
		// under it look like a retry of that same leg — money.Service would
		// then idempotently short-circuit and return the earlier transfer
		// instead of ever attempting the INSERT that the unique index needs
		// to see, silently swallowing the exact duplicate the brief wants
		// rejected with a 409.
		key := uuid.NewString()

		txInput := PaymentTxInput{
			OrganizationID: input.OrganizationID,
			TenantID:       inv.tenantID,
			PropertyID:     inv.propertyID,
			InvoiceID:      inv.id,
			Amount:         applied,
			Method:         legacyMethod,
			Reference:      input.ReferenceNumber,
			Narrative:      "Manual payment recorded by Manager",
			IdempotencyKey: key,
			RecordedBy:     input.RecordedBy,
		}
		if firstLeg {
			txInput.ManualPaymentMethod = input.Method
			txInput.ReferenceNumber = input.ReferenceNumber
			txInput.ReceiptPhotoURL = input.ReceiptPhotoURL
		}

		var transfer Transfer
		switch inv.invoiceType {
		case "rent":
			transfer, err = s.ExecuteRentPaymentTx(ctx, txInput)
		case "water_garbage":
			transfer, err = s.ExecuteWaterGarbageTx(ctx, txInput)
		default:
			continue
		}
		if err != nil {
			if isUniqueViolation(err) {
				return AllocationResult{}, ErrDuplicateReferenceNumber
			}
			return AllocationResult{}, fmt.Errorf("post %s payment for invoice %s: %w", inv.invoiceType, inv.id, err)
		}
		firstLeg = false

		result.Allocations = append(result.Allocations, AllocationLeg{
			InvoiceID:   inv.id,
			InvoiceType: inv.invoiceType,
			PeriodMonth: inv.periodMonth,
			Applied:     applied,
			Transfer:    transfer,
		})

		remaining -= applied
	}

	result.UnallocatedAmount = remaining
	return result, nil
}
