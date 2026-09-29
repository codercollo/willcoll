package data

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"encoding/base32"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/codercollo/willcoll/backend/internal/db"
	"github.com/codercollo/willcoll/backend/internal/db/sqlc"
	"github.com/codercollo/willcoll/backend/internal/reconciliation"
	"github.com/codercollo/willcoll/backend/pkg/moneyfmt"
	"github.com/google/uuid"
)

// Limits on the public pay flow.
const (
	otpDigits          = 6
	otpMaxAttempts     = 5
	otpWindow          = 10 * time.Minute
	otpMaxPerWindow    = 3
	maxIntentShillings = 250000 // the M-Pesa per-transaction ceiling
)

var (
	// ErrTooManyCodes is returned when a phone asks for too many codes.
	ErrTooManyCodes = errors.New("too many codes requested; try again later")

	// ErrInvalidCode is returned for a wrong, expired or used code. It never
	// says which, so a guess learns nothing.
	ErrInvalidCode = errors.New("invalid or expired code")

	// ErrPropertyNotLinked is returned when a property has no PayHero
	// collections channel, so it cannot take payments yet.
	ErrPropertyNotLinked = errors.New("this property is not set up to take payments")
)

// PayAccessModel is the service layer for the public tenant pay page: it
// serves a unit (found from its pay URL) to a payer who proves they hold a
// phone on the lease. It only ever touches one unit, inside that manager's
// tenant transaction.
type PayAccessModel struct {
	Store db.Store

	// Timeout bounds each method call; zero means DefaultQueryTimeout.
	Timeout time.Duration

	// Now returns the current time; nil means time.Now. Tests set it.
	Now func() time.Time
}

func (m PayAccessModel) now() time.Time {
	if m.Now != nil {
		return m.Now()
	}
	return time.Now()
}

// PayInfo is what the pay page shows before anyone has proved who they are:
// nothing about the tenant.
type PayInfo struct {
	PropertyName string `json:"property_name"`
	UnitCode     string `json:"unit_code"`
	CanPay       bool   `json:"can_pay"`
}

// Info returns the public display details of the unit.
func (m PayAccessModel) Info(ctx context.Context, t PayTarget) (*PayInfo, error) {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	var out *PayInfo
	err := m.Store.ExecTenantTx(ctx, t.TenantID, func(q sqlc.Querier) error {
		unit, err := q.GetUnit(ctx, sqlc.GetUnitParams{TenantID: t.TenantID, ID: t.UnitID})
		if err != nil {
			return notFound(err)
		}
		property, err := q.GetProperty(ctx, sqlc.GetPropertyParams{TenantID: t.TenantID, ID: t.PropertyID})
		if err != nil {
			return notFound(err)
		}
		out = &PayInfo{PropertyName: property.Name, UnitCode: unit.UnitCode,
			CanPay: property.PayheroChannelID != nil && *property.PayheroChannelID != ""}
		return nil
	})
	return out, err
}

// OTPHasher turns a code into the value that is stored, keyed so a stolen
// table does not reveal codes. Supplied by the caller, who holds the secret.
type OTPHasher func(unitID uuid.UUID, phone, code string) string

// IssueOTP creates a one-time code for phone on the unit, if that phone is on
// the active lease. issued is false (with a nil error) for any other number,
// so the endpoint cannot be used to discover who lives where. A phone may ask
// for only a few codes per window. The plain code is returned once, for the
// SMS; only its hash is stored.
func (m PayAccessModel) IssueOTP(ctx context.Context, t PayTarget, phone string, hash OTPHasher, ttl time.Duration) (code string, issued bool, err error) {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	phone = reconciliation.NormalizePhone(phone)
	if phone == "" {
		return "", false, nil
	}

	err = m.Store.ExecTenantTx(ctx, t.TenantID, func(q sqlc.Querier) error {
		code, issued = "", false

		phones, err := q.ListUnitPayPhones(ctx, sqlc.ListUnitPayPhonesParams{TenantID: t.TenantID, UnitID: t.UnitID})
		if err != nil {
			return err
		}
		known := false
		for _, p := range phones {
			if reconciliation.NormalizePhone(p) == phone {
				known = true
				break
			}
		}
		if !known {
			return nil
		}

		now := m.now()
		n, err := q.CountRecentPayOTPs(ctx, sqlc.CountRecentPayOTPsParams{
			TenantID: t.TenantID, UnitID: t.UnitID, Phone: phone, CreatedAt: now.Add(-otpWindow),
		})
		if err != nil {
			return err
		}
		if n >= otpMaxPerWindow {
			return ErrTooManyCodes
		}

		c, err := randomDigits(otpDigits)
		if err != nil {
			return err
		}
		if err := q.CreatePayOTP(ctx, sqlc.CreatePayOTPParams{
			TenantID: t.TenantID, UnitID: t.UnitID, Phone: phone, CodeHash: hash(t.UnitID, phone, c), ExpiresAt: now.Add(ttl),
		}); err != nil {
			return err
		}
		// Housekeeping: codes are worthless after a day.
		if err := q.PurgePayOTPs(ctx, sqlc.PurgePayOTPsParams{TenantID: t.TenantID, CreatedAt: now.Add(-24 * time.Hour)}); err != nil {
			return err
		}
		code, issued = c, true
		return nil
	})
	return code, issued, err
}

// VerifyOTP checks a code. A correct, unexpired, unused code is consumed and
// reported valid. Each wrong guess counts against the code, which locks after
// a few. Every failure is the same ErrInvalidCode.
func (m PayAccessModel) VerifyOTP(ctx context.Context, t PayTarget, phone, code string, hash OTPHasher) (string, error) {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	phone = reconciliation.NormalizePhone(phone)
	code = strings.TrimSpace(code)
	if phone == "" || code == "" {
		return "", ErrInvalidCode
	}

	valid := false
	err := m.Store.ExecTenantTx(ctx, t.TenantID, func(q sqlc.Querier) error {
		valid = false
		otp, err := q.GetLatestPayOTP(ctx, sqlc.GetLatestPayOTPParams{TenantID: t.TenantID, UnitID: t.UnitID, Phone: phone})
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			return err
		}
		if otp.ConsumedAt != nil || otp.Attempts >= otpMaxAttempts || !m.now().Before(otp.ExpiresAt) {
			return nil
		}
		// Count the guess before judging it, so it cannot be retried for free.
		if err := q.BumpPayOTPAttempts(ctx, sqlc.BumpPayOTPAttemptsParams{TenantID: t.TenantID, ID: otp.ID}); err != nil {
			return err
		}
		want := hash(t.UnitID, phone, code)
		if subtle.ConstantTimeCompare([]byte(want), []byte(otp.CodeHash)) != 1 {
			return nil
		}
		n, err := q.ConsumePayOTP(ctx, sqlc.ConsumePayOTPParams{TenantID: t.TenantID, ID: otp.ID})
		if err != nil {
			return err
		}
		valid = n == 1
		return nil
	})
	if err != nil {
		return "", err
	}
	if !valid {
		return "", ErrInvalidCode
	}
	return phone, nil
}

func randomDigits(n int) (string, error) {
	var b strings.Builder
	for i := 0; i < n; i++ {
		d, err := rand.Int(rand.Reader, big.NewInt(10))
		if err != nil {
			return "", err
		}
		b.WriteByte(byte('0' + d.Int64()))
	}
	return b.String(), nil
}

// PayLine is one thing the payer may pay towards.
type PayLine struct {
	Type    string         `json:"type"`
	Balance moneyfmt.Money `json:"balance"`
	Payable bool           `json:"payable"`
}

// payLineOrder is the order lines are offered in.
var payLineOrder = []string{LedgerTypeRent, LedgerTypeWater, LedgerTypeGarbage, LedgerTypeRentDeposit, LedgerTypeWaterDeposit, LedgerTypeElectricityDeposit}

// isDeposit reports whether a ledger type is a one-off refundable deposit,
// as opposed to a recurring bill: a deposit can't be paid ahead (capped at
// its outstanding balance), where rent/water/garbage can.
func isDeposit(ledgerType string) bool {
	switch ledgerType {
	case LedgerTypeRentDeposit, LedgerTypeWaterDeposit, LedgerTypeElectricityDeposit:
		return true
	}
	return false
}

// shown reports whether a ledger type has a line on the pay page at all:
// rent, water, rent deposit and water deposit always (a settled deposit
// still shows, just "Paid" and un-payable — the tenant can see it was
// covered), garbage only where the property bills it, electricity deposit
// only where the property has it enabled. Distinct from payable, which is
// about whether that line can be paid RIGHT NOW.
func shown(ledgerType string, garbageEnabled, electricityEnabled bool) bool {
	switch ledgerType {
	case LedgerTypeRent, LedgerTypeWater, LedgerTypeRentDeposit, LedgerTypeWaterDeposit:
		return true
	case LedgerTypeGarbage:
		return garbageEnabled
	case LedgerTypeElectricityDeposit:
		return electricityEnabled
	}
	return false
}

// payable reports whether a shown line can currently take a payment: rent,
// water and garbage always (paying ahead is normal), a deposit only while
// something is still owed on it — a one-off refundable deposit can't be
// paid ahead (system-design.txt 4.7, 3.2.1).
func payable(ledgerType string, balance moneyfmt.Money) bool {
	switch ledgerType {
	case LedgerTypeRent, LedgerTypeWater, LedgerTypeGarbage:
		return true
	case LedgerTypeRentDeposit, LedgerTypeWaterDeposit, LedgerTypeElectricityDeposit:
		return balance.IsPositive()
	}
	return false
}

// Balances returns the unit's payable lines with what is owed on each.
func (m PayAccessModel) Balances(ctx context.Context, t PayTarget) ([]PayLine, error) {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	var out []PayLine
	err := m.Store.ExecTenantTx(ctx, t.TenantID, func(q sqlc.Querier) error {
		var err error
		out, err = payLines(ctx, q, t)
		return err
	})
	return out, err
}

func payLines(ctx context.Context, q sqlc.Querier, t PayTarget) ([]PayLine, error) {
	property, err := q.GetProperty(ctx, sqlc.GetPropertyParams{TenantID: t.TenantID, ID: t.PropertyID})
	if err != nil {
		return nil, notFound(err)
	}
	rows, err := q.ListUnitBalances(ctx, sqlc.ListUnitBalancesParams{TenantID: t.TenantID, UnitID: t.UnitID})
	if err != nil {
		return nil, err
	}
	balances := map[string]moneyfmt.Money{}
	for _, r := range rows {
		balances[r.Type] = r.Balance
	}
	var out []PayLine
	for _, lt := range payLineOrder {
		b := balances[lt]
		if shown(lt, property.GarbageEnabled, property.ElectricityEnabled) {
			out = append(out, PayLine{Type: lt, Balance: b, Payable: payable(lt, b)})
		}
	}
	return out, nil
}

// IntentLine is one requested part of a payment.
type IntentLine struct {
	Type   string         `json:"type"`
	Amount moneyfmt.Money `json:"amount"`
}

// CreatedIntent is a stored intent ready for its STK push.
type CreatedIntent struct {
	ID           uuid.UUID
	Reference    string // the external reference sent to PayHero
	Amount       moneyfmt.Money
	Shillings    int64
	ChannelID    string
	PropertyName string
	UnitCode     string
	ExpiresAt    time.Time
}

// CreateIntent validates and stores a payment intent: the exact split the
// payer wants, applied verbatim when the money arrives (Track A). Lines must
// be offered ones, positive, unique, and add up to a whole number of shillings
// (an STK push cannot carry cents) within the M-Pesa limit.
func (m PayAccessModel) CreateIntent(ctx context.Context, t PayTarget, phone string, lines []IntentLine, ttl time.Duration) (*CreatedIntent, error) {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	phone = reconciliation.NormalizePhone(phone)
	if !strings.HasPrefix(phone, "+254") || len(phone) != 13 {
		return nil, fmt.Errorf("%w: phone must be a Kenyan mobile number", ErrInvalidInput)
	}
	if len(lines) == 0 {
		return nil, fmt.Errorf("%w: choose at least one thing to pay", ErrInvalidInput)
	}

	var out *CreatedIntent
	err := m.Store.ExecTenantTx(ctx, t.TenantID, func(q sqlc.Querier) error {
		property, err := q.GetProperty(ctx, sqlc.GetPropertyParams{TenantID: t.TenantID, ID: t.PropertyID})
		if err != nil {
			return notFound(err)
		}
		if property.PayheroChannelID == nil || *property.PayheroChannelID == "" {
			return ErrPropertyNotLinked
		}
		unit, err := q.GetUnit(ctx, sqlc.GetUnitParams{TenantID: t.TenantID, ID: t.UnitID})
		if err != nil {
			return notFound(err)
		}
		avail, err := payLines(ctx, q, t)
		if err != nil {
			return err
		}
		allowed := map[string]bool{}
		balance := map[string]moneyfmt.Money{}
		for _, l := range avail {
			allowed[l.Type] = l.Payable
			balance[l.Type] = l.Balance
		}

		var total moneyfmt.Money
		seen := map[string]bool{}
		stored := make([]IntentLine, 0, len(lines))
		for _, l := range lines {
			lt := strings.ToUpper(strings.TrimSpace(l.Type))
			switch {
			case !allowed[lt]:
				return fmt.Errorf("%w: %s cannot be paid here", ErrInvalidInput, lt)
			case !l.Amount.IsPositive():
				return fmt.Errorf("%w: %s amount must be greater than zero", ErrInvalidInput, lt)
			case seen[lt]:
				return fmt.Errorf("%w: %s appears twice", ErrInvalidInput, lt)
			case isDeposit(lt) && l.Amount.Cmp(balance[lt]) > 0:
				// A deposit can't be paid ahead: cap it at what's outstanding
				// (system-design.txt 3.2.1). Rent/water/garbage may still be
				// overpaid, which is normal (an advance).
				return fmt.Errorf("%w: %s amount must not be more than the %s outstanding", ErrInvalidInput, lt, balance[lt].Display())
			}
			seen[lt] = true
			total = total.Add(l.Amount)
			stored = append(stored, IntentLine{Type: lt, Amount: l.Amount})
		}
		if total.Cents()%100 != 0 {
			return fmt.Errorf("%w: the total must be a whole number of shillings", ErrInvalidInput)
		}
		shillings := total.Cents() / 100
		if shillings > maxIntentShillings {
			return fmt.Errorf("%w: the total must not be more than %d", ErrInvalidInput, maxIntentShillings)
		}

		raw, err := json.Marshal(stored)
		if err != nil {
			return err
		}

		// Resubmitting the same payment (e.g. after an ambiguous PayHero
		// timeout, where the first STK push may already be out) reuses the
		// still-pending, unexpired intent for this unit rather than minting
		// a fresh reference — so the retry can only ever produce one prompt
		// per reference, and the webhook can't double-post it.
		if existing, err := q.GetPendingIntentForUnit(ctx, sqlc.GetPendingIntentForUnitParams{
			TenantID: t.TenantID, UnitID: t.UnitID, Status: PaymentIntentStatusPending, ExpiresAt: m.now(), Lines: raw,
		}); err == nil {
			out = &CreatedIntent{ID: existing.ID, Reference: existing.ExternalReference, Amount: total, Shillings: shillings,
				ChannelID: *property.PayheroChannelID, PropertyName: property.Name, UnitCode: unit.UnitCode, ExpiresAt: existing.ExpiresAt}
			return nil
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err
		}

		ref, err := newIntentReference()
		if err != nil {
			return err
		}
		expires := m.now().Add(ttl)
		row, err := q.CreatePaymentIntent(ctx, sqlc.CreatePaymentIntentParams{
			TenantID: t.TenantID, UnitID: t.UnitID, Lines: raw, ExternalReference: ref, Phone: phone,
			Status: PaymentIntentStatusPending, ExpiresAt: expires,
		})
		if err != nil {
			return err
		}
		out = &CreatedIntent{ID: row.ID, Reference: ref, Amount: total, Shillings: shillings,
			ChannelID: *property.PayheroChannelID, PropertyName: property.Name, UnitCode: unit.UnitCode, ExpiresAt: expires}
		return nil
	})
	return out, err
}

// newIntentReference returns a fresh unguessable reference such as
// WC-7QK2M4XH9BTA, unique among intents.
func newIntentReference() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "WC-" + base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b)[:12], nil
}

// RecordCheckout stores PayHero checkout request id against an intent.
func (m PayAccessModel) RecordCheckout(ctx context.Context, tenantID, intentID uuid.UUID, checkoutID string) error {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()
	return m.Store.ExecTenantTx(ctx, tenantID, func(q sqlc.Querier) error {
		return q.SetPaymentIntentCheckout(ctx, sqlc.SetPaymentIntentCheckoutParams{
			TenantID: tenantID, ID: intentID, CheckoutRequestID: &checkoutID,
		})
	})
}

// FailIntent marks an intent failed, for a push that could not be sent.
func (m PayAccessModel) FailIntent(ctx context.Context, tenantID, intentID uuid.UUID, reason string) error {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()
	return m.Store.ExecTenantTx(ctx, tenantID, func(q sqlc.Querier) error {
		return q.SetPaymentIntentStatus(ctx, sqlc.SetPaymentIntentStatusParams{
			TenantID: tenantID, ID: intentID, Status: PaymentIntentStatusFailed, FailureReason: &reason,
		})
	})
}

// IntentStatus is what the payer polls.
type IntentStatus struct {
	Status        string      `json:"status"`
	FailureReason string      `json:"failure_reason,omitempty"`
	Receipt       *PayReceipt `json:"receipt,omitempty"`
}

// PayReceipt is the itemised receipt a tenant sees when their payment has gone
// through: what was paid towards each ledger, and the M-Pesa reference.
type PayReceipt struct {
	MpesaReceipt string         `json:"mpesa_receipt"`
	Amount       moneyfmt.Money `json:"amount"`
	PaidAt       time.Time      `json:"paid_at"`
	PropertyName string         `json:"property_name"`
	UnitCode     string         `json:"unit_code"`
	Lines        []IntentLine   `json:"lines"`
}

// Status reads an intent belonging to the unit, first closing any whose time
// has run out. An intent of another unit reads as not found.
func (m PayAccessModel) Status(ctx context.Context, tenantID, unitID, intentID uuid.UUID) (*IntentStatus, error) {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	var out *IntentStatus
	err := m.Store.ExecTenantTx(ctx, tenantID, func(q sqlc.Querier) error {
		if _, err := q.ExpireStaleIntents(ctx, tenantID); err != nil {
			return err
		}
		intent, err := q.GetPaymentIntent(ctx, sqlc.GetPaymentIntentParams{TenantID: tenantID, ID: intentID})
		if err != nil {
			return notFound(err)
		}
		if intent.UnitID != unitID {
			return ErrRecordNotFound
		}
		out = &IntentStatus{Status: intent.Status}
		if intent.FailureReason != nil {
			out.FailureReason = *intent.FailureReason
		}
		if intent.Status == PaymentIntentStatusCompleted {
			receipt, err := intentReceipt(ctx, q, tenantID, intent)
			if err != nil {
				return err
			}
			out.Receipt = receipt
		}
		return nil
	})
	return out, err
}

// intentReceipt builds the receipt for a completed intent. Its lines are the
// split the payer chose, which is exactly what was applied when the intent
// completed (an intent only completes when its lines add up to the payment).
func intentReceipt(ctx context.Context, q sqlc.Querier, tenantID uuid.UUID, intent sqlc.PaymentIntent) (*PayReceipt, error) {
	payment, err := q.GetPaymentForIntent(ctx, sqlc.GetPaymentForIntentParams{TenantID: tenantID, MatchedIntentID: &intent.ID})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil // completed, but the payment row is not visible: no receipt rather than an error
		}
		return nil, err
	}
	unit, err := q.GetUnit(ctx, sqlc.GetUnitParams{TenantID: tenantID, ID: intent.UnitID})
	if err != nil {
		return nil, notFound(err)
	}
	property, err := q.GetProperty(ctx, sqlc.GetPropertyParams{TenantID: tenantID, ID: unit.PropertyID})
	if err != nil {
		return nil, notFound(err)
	}
	lines := []IntentLine{}
	if err := json.Unmarshal(intent.Lines, &lines); err != nil {
		return nil, err
	}
	return &PayReceipt{
		MpesaReceipt: payment.MpesaReceipt, Amount: payment.Amount, PaidAt: payment.ReceivedAt,
		PropertyName: property.Name, UnitCode: unit.UnitCode, Lines: lines,
	}, nil
}
