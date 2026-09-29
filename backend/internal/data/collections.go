package data

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/codercollo/willcoll/backend/internal/db"
	"github.com/codercollo/willcoll/backend/internal/db/sqlc"
	"github.com/codercollo/willcoll/backend/internal/reconciliation"
	"github.com/codercollo/willcoll/backend/pkg/moneyfmt"
	"github.com/google/uuid"
)

var (
	// ErrUnroutable is returned when an inbound payment can be tied to no
	// manager: its reference matches no intent and its channel matches no
	// property (or several managers). Nothing can be recorded for it.
	ErrUnroutable = errors.New("payment cannot be routed to a manager")
)

// ResolverModel finds which manager an inbound request belongs to. Webhooks
// and public pay pages arrive with no tenant context, so these are the only
// cross-tenant reads in the API: three narrow, id-only, SELECT-only queries
// on the willcoll_admin pool (BYPASSRLS). Everything after the lookup runs
// on the RLS-scoped pool for the resolved tenant.
type ResolverModel struct {
	Store db.Store

	// Timeout bounds each method call; zero means DefaultQueryTimeout.
	Timeout time.Duration
}

// IntentRef identifies a payment intent across tenants.
type IntentRef struct {
	IntentID uuid.UUID
	TenantID uuid.UUID
	UnitID   uuid.UUID
}

// PayTarget identifies the unit a public pay URL points at.
type PayTarget struct {
	TenantID   uuid.UUID
	PropertyID uuid.UUID
	UnitID     uuid.UUID
}

// IntentByReference resolves an STK external reference. ok is false when it
// names no intent.
func (m ResolverModel) IntentByReference(ctx context.Context, reference string) (ref IntentRef, ok bool, err error) {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	row, err := m.Store.ResolveIntentByReference(ctx, reference)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return IntentRef{}, false, nil
		}
		return IntentRef{}, false, err
	}
	return IntentRef{IntentID: row.ID, TenantID: row.TenantID, UnitID: row.UnitID}, true, nil
}

// TenantForChannel resolves a PayHero collections channel to its manager.
// It returns ErrUnroutable if the channel is unknown or shared by several
// managers.
func (m ResolverModel) TenantForChannel(ctx context.Context, channelID string) (uuid.UUID, error) {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	if channelID == "" {
		return uuid.Nil, ErrUnroutable
	}
	tenants, err := m.Store.ResolveChannelTenants(ctx, &channelID)
	if err != nil {
		return uuid.Nil, err
	}
	if len(tenants) != 1 {
		return uuid.Nil, ErrUnroutable
	}
	return tenants[0], nil
}

// PayTarget resolves /pay/:propertySlug/:unitCode, or ErrRecordNotFound.
func (m ResolverModel) PayTarget(ctx context.Context, propertySlug, unitCode string) (*PayTarget, error) {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	row, err := m.Store.ResolvePayTarget(ctx, sqlc.ResolvePayTargetParams{Slug: propertySlug, UnitCode: unitCode})
	if err != nil {
		return nil, notFound(err)
	}
	return &PayTarget{TenantID: row.TenantID, PropertyID: row.PropertyID, UnitID: row.UnitID}, nil
}

// CollectionPayment is one normalised inbound PayHero collection, whatever
// the wire format it arrived in.
type CollectionPayment struct {
	MpesaReceipt     string
	Amount           moneyfmt.Money
	Msisdn           string
	PayerName        string
	AccountReference string // the STK external reference, or a bank account number
	ChannelID        string
	PayheroReference string
	ReceivedAt       time.Time
	Raw              json.RawMessage
}

// IngestResult reports what became of a payment.
type IngestResult struct {
	PaymentID   uuid.UUID
	Duplicate   bool   // PayHero redelivered a payment already recorded
	Status      string // unmatched, matched or allocated
	UnitID      *uuid.UUID
	Method      string // intent, phone, name or ""
	NeedsReview bool

	// What a confirmation SMS needs, set when the payment was applied.
	Applied      bool
	Receipt      string
	Amount       moneyfmt.Money
	Msisdn       string
	PropertyName string
	UnitCode     string
}

// CollectionsModel ingests PayHero collections: dedupe, match, allocate.
type CollectionsModel struct {
	Store    db.Store
	Resolver ResolverModel

	// Timeout bounds each method call; zero means DefaultQueryTimeout.
	Timeout time.Duration
}

type intentLine struct {
	Type   string         `json:"type"`
	Amount moneyfmt.Money `json:"amount"`
}

// Process records one inbound collection and applies it to the ledger, all
// in one serializable transaction (retried on serialization failure), so a
// payment is either fully recorded and placed or not recorded at all and
// PayHero's retry will try again (system-design.txt 3.8, 4.8, 5):
//
//  1. Insert with ON CONFLICT (mpesa_receipt) DO NOTHING; no row means a
//     redelivery, which is acknowledged and otherwise ignored.
//  2. Track A: the reference names a pending intent whose lines add up to the
//     amount: apply the lines verbatim, no inference.
//  3. Track B: otherwise identify the unit from the payer's phone, then name.
//     With the unit known, subset-sum the amount against its open balances:
//     exactly one combination applies automatically; none applies the default
//     waterfall, flagged unconfirmed; several leave it unmatched (unit noted)
//     for a manager to resolve.
//  4. Nobody identified: recorded as unmatched, in the review queue.
//
// A unit identified only by a fuzzy name match is applied but flagged
// unconfirmed, since attributing money to the wrong unit is the costly error.
func (m CollectionsModel) Process(ctx context.Context, in CollectionPayment) (*IngestResult, error) {
	ctx, cancel := withTimeout(ctx, postingTimeout(m.Timeout))
	defer cancel()

	in.MpesaReceipt = strings.TrimSpace(in.MpesaReceipt)
	if in.MpesaReceipt == "" || !in.Amount.IsPositive() {
		return nil, fmt.Errorf("%w: a payment needs a receipt and a positive amount", ErrInvalidInput)
	}
	if in.ReceivedAt.IsZero() {
		in.ReceivedAt = time.Now()
	}
	if len(in.Raw) == 0 {
		in.Raw = json.RawMessage(`{}`)
	}

	// Which manager is this for?
	var tenantID uuid.UUID
	source := PaymentSourcePayheroC2B
	if in.AccountReference != "" {
		ref, ok, err := m.Resolver.IntentByReference(ctx, in.AccountReference)
		if err != nil {
			return nil, err
		}
		if ok {
			tenantID, source = ref.TenantID, PaymentSourcePayheroSTK
		}
	}
	if tenantID == uuid.Nil {
		var err error
		if tenantID, err = m.Resolver.TenantForChannel(ctx, in.ChannelID); err != nil {
			return nil, err
		}
	}

	var result IngestResult
	// Payments for one manager post one at a time: simultaneous callbacks would
	// otherwise conflict under SERIALIZABLE and could exhaust their retries.
	err := m.Store.ExecTenantTxExclusive(ctx, tenantID, lockPayments, func(q sqlc.Querier) error {
		result = IngestResult{}
		return m.ingest(ctx, q, tenantID, source, in, &result)
	})
	if err != nil {
		return nil, err
	}
	return &result, nil
}

func (m CollectionsModel) ingest(ctx context.Context, q sqlc.Querier, tenantID uuid.UUID, source string, in CollectionPayment, result *IngestResult) error {
	var payerName, account, payheroRef *string
	if in.PayerName != "" {
		payerName = &in.PayerName
	}
	if in.AccountReference != "" {
		account = &in.AccountReference
	}
	if in.PayheroReference != "" {
		payheroRef = &in.PayheroReference
	}

	payment, err := q.CreatePayment(ctx, sqlc.CreatePaymentParams{
		TenantID: tenantID, Source: source, MpesaReceipt: in.MpesaReceipt, PayheroReference: payheroRef,
		Amount: in.Amount, Msisdn: in.Msisdn, PayerName: payerName, AccountReference: account,
		Status: PaymentStatusUnmatched, RawPayload: in.Raw, ReceivedAt: in.ReceivedAt,
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			result.Duplicate = true
			return nil
		}
		return err
	}
	result.PaymentID = payment.ID
	result.Receipt, result.Amount, result.Msisdn = in.MpesaReceipt, in.Amount, in.Msisdn

	var (
		unitID   uuid.UUID
		method   string
		intentID *uuid.UUID
		note     string
		guessed  bool // unit inferred from a fuzzy name
	)

	// Track A: the reference names an intent.
	if in.AccountReference != "" {
		intent, err := q.GetPaymentIntentByReference(ctx, sqlc.GetPaymentIntentByReferenceParams{TenantID: tenantID, ExternalReference: in.AccountReference})
		switch {
		case err == nil:
			unitID, method, intentID = intent.UnitID, "intent", &intent.ID
			lines, ok := parseIntentLines(intent.Lines, in.Amount)
			if intent.Status == PaymentIntentStatusPending && ok {
				if err := postPaymentAllocations(ctx, q, tenantID, payment, unitID, lines, "system",
					PaymentOutcome{Status: PaymentStatusAllocated, IntentID: intentID}); err != nil {
					return err
				}
				if err := q.SetPaymentIntentStatus(ctx, sqlc.SetPaymentIntentStatusParams{
					TenantID: tenantID, ID: intent.ID, Status: PaymentIntentStatusCompleted,
				}); err != nil {
					return err
				}
				result.Status, result.UnitID, result.Method = PaymentStatusAllocated, &unitID, method
				result.Applied = true
				return describeUnit(ctx, q, tenantID, unitID, result)
			}
			note = "intent lines do not match the amount paid; applied by balance"
			if intent.Status != PaymentIntentStatusPending {
				note = "intent was no longer pending (" + intent.Status + "); applied by balance"
			}
		case errors.Is(err, sql.ErrNoRows):
			// A bank account number, not an intent: Track B.
		default:
			return err
		}
	}

	// Track B: identify the unit.
	if method == "" {
		match, err := m.identify(ctx, q, tenantID, in)
		if err != nil {
			return err
		}
		if !match.Found() {
			note = "no unit could be identified from the payer phone or name"
			if match.Ambiguous {
				note = "the payer matches more than one unit"
			}
			result.Status, result.NeedsReview = PaymentStatusUnmatched, true
			return q.SetPaymentMatch(ctx, sqlc.SetPaymentMatchParams{
				TenantID: tenantID, ID: payment.ID, Status: PaymentStatusUnmatched, ReviewNote: &note,
			})
		}
		unitID, err = uuid.Parse(match.UnitID)
		if err != nil {
			return err
		}
		method, guessed = string(match.Method), match.Method == reconciliation.ByName
	}
	result.UnitID, result.Method = &unitID, method

	// Unit known: split the amount across what it owes.
	balances, err := q.ListUnitBalances(ctx, sqlc.ListUnitBalancesParams{TenantID: tenantID, UnitID: unitID})
	if err != nil {
		return err
	}
	open := make([]reconciliation.OpenBalance, 0, len(balances))
	for _, b := range balances {
		open = append(open, reconciliation.OpenBalance{LedgerType: b.Type, Balance: b.Balance})
	}

	combos := reconciliation.FindMatchingCombinations(in.Amount, open)
	switch len(combos) {
	case 1:
		outcome := PaymentOutcome{Status: PaymentStatusAllocated, IntentID: intentID, Note: note}
		if guessed {
			outcome.Status, outcome.Unconfirmed = PaymentStatusMatched, true
			outcome.Note = joinNotes(note, "unit identified by payer name only")
		}
		result.NeedsReview = outcome.Unconfirmed
		result.Status = outcome.Status
		if err := postPaymentAllocations(ctx, q, tenantID, payment, unitID, toAllocations(combos[0]), "system", outcome); err != nil {
			return err
		}
		result.Applied = true
		return describeUnit(ctx, q, tenantID, unitID, result)
	case 0:
		outcome := PaymentOutcome{
			Status: PaymentStatusMatched, IntentID: intentID, Unconfirmed: true,
			Note: joinNotes(note, "no exact combination of open balances; applied by the default waterfall"),
		}
		result.Status, result.NeedsReview = outcome.Status, true
		if err := postPaymentAllocations(ctx, q, tenantID, payment, unitID, toAllocations(reconciliation.ApplyWaterfall(in.Amount, open)), "system", outcome); err != nil {
			return err
		}
		result.Applied = true
		return describeUnit(ctx, q, tenantID, unitID, result)
	default:
		review := joinNotes(note, fmt.Sprintf("the amount fits %d combinations of open balances; a manager must choose", len(combos)))
		result.Status, result.NeedsReview = PaymentStatusUnmatched, true
		return q.SetPaymentMatch(ctx, sqlc.SetPaymentMatchParams{
			TenantID: tenantID, ID: payment.ID, Status: PaymentStatusUnmatched, MatchedUnitID: &unitID,
			MatchedIntentID: intentID, ReviewNote: &review,
		})
	}
}

// identify finds the unit a payment belongs to among the units on the
// payment's collections channel.
func (m CollectionsModel) identify(ctx context.Context, q sqlc.Querier, tenantID uuid.UUID, in CollectionPayment) (reconciliation.UnitMatch, error) {
	rows, err := q.ListPayerCandidates(ctx, sqlc.ListPayerCandidatesParams{TenantID: tenantID, ChannelID: &in.ChannelID})
	if err != nil {
		return reconciliation.UnitMatch{}, err
	}
	byUnit := map[string]*reconciliation.Candidate{}
	var order []string
	for _, r := range rows {
		id := r.UnitID.String()
		c, ok := byUnit[id]
		if !ok {
			c = &reconciliation.Candidate{UnitID: id}
			byUnit[id] = c
			order = append(order, id)
		}
		if r.Kind == "phone" {
			c.Phones = append(c.Phones, r.Value)
		} else {
			c.Names = append(c.Names, r.Value)
		}
	}
	cands := make([]reconciliation.Candidate, 0, len(order))
	for _, id := range order {
		cands = append(cands, *byUnit[id])
	}
	return reconciliation.MatchUnit(in.Msisdn, in.PayerName, cands), nil
}

// FailIntent records that an STK push failed or was cancelled, unless the
// intent already completed. Unknown references are ignored.
func (m CollectionsModel) FailIntent(ctx context.Context, reference, reason string) error {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	ref, ok, err := m.Resolver.IntentByReference(ctx, reference)
	if err != nil || !ok {
		return err
	}
	return m.Store.ExecTenantTx(ctx, ref.TenantID, func(q sqlc.Querier) error {
		intent, err := q.GetPaymentIntentByReference(ctx, sqlc.GetPaymentIntentByReferenceParams{TenantID: ref.TenantID, ExternalReference: reference})
		if err != nil {
			return notFound(err)
		}
		if intent.Status != PaymentIntentStatusPending {
			return nil
		}
		return q.SetPaymentIntentStatus(ctx, sqlc.SetPaymentIntentStatusParams{
			TenantID: ref.TenantID, ID: intent.ID, Status: PaymentIntentStatusFailed, FailureReason: &reason,
		})
	})
}

// parseIntentLines reads an intent lines document and reports whether it is
// valid and adds up to exactly the amount paid.
func parseIntentLines(raw json.RawMessage, paid moneyfmt.Money) ([]Allocation, bool) {
	var lines []intentLine
	if err := json.Unmarshal(raw, &lines); err != nil || len(lines) == 0 {
		return nil, false
	}
	var sum moneyfmt.Money
	out := make([]Allocation, 0, len(lines))
	for _, l := range lines {
		lt := strings.ToUpper(l.Type)
		if !manualLedgerTypes[lt] || !l.Amount.IsPositive() {
			return nil, false
		}
		sum = sum.Add(l.Amount)
		out = append(out, Allocation{LedgerType: lt, Amount: l.Amount})
	}
	return out, sum == paid
}

func toAllocations(in []reconciliation.Allocation) []Allocation {
	out := make([]Allocation, len(in))
	for i, a := range in {
		out[i] = Allocation{LedgerType: a.LedgerType, Amount: a.Amount}
	}
	return out
}

func joinNotes(parts ...string) string {
	var out []string
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, "; ")
}

// describeUnit fills the unit and property names a confirmation SMS quotes.
func describeUnit(ctx context.Context, q sqlc.Querier, tenantID, unitID uuid.UUID, result *IngestResult) error {
	unit, err := q.GetUnit(ctx, sqlc.GetUnitParams{TenantID: tenantID, ID: unitID})
	if err != nil {
		return err
	}
	property, err := q.GetProperty(ctx, sqlc.GetPropertyParams{TenantID: tenantID, ID: unit.PropertyID})
	if err != nil {
		return err
	}
	result.UnitCode, result.PropertyName = unit.UnitCode, property.Name
	return nil
}

// SubscriptionInvoice resolves a subscription invoice reference to its
// manager. ok is false when it names no invoice.
func (m ResolverModel) SubscriptionInvoice(ctx context.Context, invoiceID uuid.UUID) (managerID uuid.UUID, ok bool, err error) {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	row, err := m.Store.ResolveSubscriptionInvoice(ctx, invoiceID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return uuid.Nil, false, nil
		}
		return uuid.Nil, false, err
	}
	return row.ManagerID, true, nil
}
