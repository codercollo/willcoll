package data

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/codercollo/willcoll/backend/internal/db"
	"github.com/codercollo/willcoll/backend/internal/db/sqlc"
	"github.com/codercollo/willcoll/backend/internal/payhero"
	"github.com/codercollo/willcoll/backend/pkg/moneyfmt"
	"github.com/google/uuid"
)

// Subscription statuses (system-design.txt 3.6).
const (
	SubscriptionTrialing  = "trialing"
	SubscriptionActive    = "active"
	SubscriptionPastDue   = "past_due"
	SubscriptionCancelled = "cancelled"
)

// Subscription invoice statuses.
const (
	InvoicePending = "pending"
	InvoicePaid    = "paid"
	InvoiceFailed  = "failed"
)

var (
	// ErrNoPlan is returned when a manager with no subscription renews
	// without choosing a plan, or names a plan that does not exist.
	ErrNoPlan = errors.New("a valid subscription plan is required")

	// ErrUnderpaid is returned when a subscription payment is smaller than
	// its invoice. The invoice stays pending and the money must be handled
	// by hand, so it is reported rather than silently accepted.
	ErrUnderpaid = errors.New("subscription payment is less than the invoice amount")

	// ErrPlanTooSmall is returned when a chosen plan's unit cap is below the
	// manager's current occupied+vacant unit count.
	ErrPlanTooSmall = errors.New("plan's unit limit is smaller than the manager's current unit count")

	// ErrPlanNotAvailable is returned when a TEST plan is renewed onto, or
	// activated, outside development.
	ErrPlanNotAvailable = errors.New("plan not available")
)

// errReceiptReused rolls back a subscription transaction whose receipt is
// already on another invoice.
var errReceiptReused = errors.New("receipt already used")

// Plan pricing types.
const (
	PricingFlat    = "flat"
	PricingPerUnit = "per_unit"
)

// Plan is a subscription plan. A flat plan charges Price; a per_unit plan
// charges max(PerUnitPrice * units, MinPrice), computed at invoice time by
// PriceForUnits.
type Plan struct {
	ID              uuid.UUID       `json:"id"`
	Name            string          `json:"name"`
	Price           moneyfmt.Money  `json:"price"`
	BillingInterval string          `json:"billing_interval"`
	UnitCap         *int32          `json:"unit_cap,omitempty"`
	IsTest          bool            `json:"is_test"`
	Archived        bool            `json:"archived"`
	PricingType     string          `json:"pricing_type"`
	PerUnitPrice    *moneyfmt.Money `json:"per_unit_price,omitempty"`
	MinPrice        *moneyfmt.Money `json:"min_price,omitempty"`
	SortOrder       int32           `json:"sort_order"`
}

// PriceForUnits is what a plan costs a manager with the given unit count.
// Flat plans ignore units; a per_unit plan is max(PerUnitPrice*units,
// MinPrice).
func (p Plan) PriceForUnits(units int32) moneyfmt.Money {
	if p.PricingType != PricingPerUnit || p.PerUnitPrice == nil {
		return p.Price
	}
	amount := moneyfmt.FromCents(p.PerUnitPrice.Cents() * int64(units))
	if p.MinPrice != nil && amount.Cmp(*p.MinPrice) < 0 {
		return *p.MinPrice
	}
	return amount
}

// Subscription is a manager platform subscription.
type Subscription struct {
	ID                 uuid.UUID `json:"id"`
	PlanID             uuid.UUID `json:"plan_id"`
	Status             string    `json:"status"`
	CurrentPeriodStart time.Time `json:"current_period_start"`
	CurrentPeriodEnd   time.Time `json:"current_period_end"`
}

// SubscriptionInvoice is what a manager is billed.
type SubscriptionInvoice struct {
	ID           uuid.UUID      `json:"id"`
	PlanID       uuid.UUID      `json:"plan_id"`
	Amount       moneyfmt.Money `json:"amount"`
	Period       time.Time      `json:"period"`
	MpesaReceipt string         `json:"mpesa_receipt,omitempty"`
	Status       string         `json:"status"`
	PaidAt       *time.Time     `json:"paid_at,omitempty"`
	CreatedAt    time.Time      `json:"created_at"`
}

// BillingModel is the service layer for Willcoll platform billing: managers
// pay a subscription through PayHero billing channel. Every manager is a
// tenant: subscriptions and invoices are RLS-scoped to them, and the webhook
// finds the manager from the invoice reference it sent.
type BillingModel struct {
	Store    db.Store
	Resolver ResolverModel

	// Timeout bounds each method call; zero means DefaultQueryTimeout.
	Timeout time.Duration

	// Now returns the current time; nil means time.Now. Tests set it.
	Now func() time.Time

	// Env is cfg.env ("development"|"staging"|"production"). A TEST plan
	// (is_test) can only be renewed onto, or activated by a webhook, when
	// this is exactly "development" — the backend is the source of truth
	// for this guard, never the frontend.
	Env string
}

func (m BillingModel) isDevelopment() bool { return m.Env == "development" }

func (m BillingModel) now() time.Time {
	if m.Now != nil {
		return m.Now()
	}
	return time.Now()
}

func today(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

func planFromRow(r sqlc.SubscriptionPlan) Plan {
	p := Plan{
		ID: r.ID, Name: r.Name, Price: r.Price, BillingInterval: r.BillingInterval,
		IsTest: r.IsTest, Archived: r.ArchivedAt != nil, PricingType: r.PricingType,
		PerUnitPrice: r.PerUnitPrice, MinPrice: r.MinPrice, SortOrder: r.SortOrder,
	}
	if r.UnitCap.Valid {
		v := r.UnitCap.Int32
		p.UnitCap = &v
	}
	return p
}

func invoiceFromRow(r sqlc.SubscriptionInvoice) SubscriptionInvoice {
	inv := SubscriptionInvoice{ID: r.ID, PlanID: r.PlanID, Amount: r.Amount, Period: r.Period, Status: r.Status, PaidAt: r.PaidAt, CreatedAt: r.CreatedAt}
	if !strings.HasPrefix(r.MpesaReceipt, "PENDING-") {
		inv.MpesaReceipt = r.MpesaReceipt
	}
	return inv
}

// addInterval moves t forward by one billing interval.
func addInterval(t time.Time, interval string) (time.Time, error) {
	switch strings.ToLower(interval) {
	case "weekly":
		return t.AddDate(0, 0, 7), nil
	case "monthly":
		return t.AddDate(0, 1, 0), nil
	case "quarterly":
		return t.AddDate(0, 3, 0), nil
	case "yearly", "annual", "annually":
		return t.AddDate(1, 0, 0), nil
	}
	return time.Time{}, fmt.Errorf("%w: unknown billing interval %q", ErrInvalidInput, interval)
}

// Plans lists the plans a manager can subscribe to, with their current
// occupied+vacant unit count so the picker can disable plans too small for
// it. includeTest shows the TEST plan too — the API sets it only when
// ENV == "development", never from a frontend flag.
func (m BillingModel) Plans(ctx context.Context, tenantID uuid.UUID) ([]Plan, int32, error) {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	var out []Plan
	var units int32
	err := m.Store.ExecTenantTx(ctx, tenantID, func(q sqlc.Querier) error {
		rows, err := q.ListSubscriptionPlans(ctx, m.isDevelopment())
		if err != nil {
			return err
		}
		out = make([]Plan, 0, len(rows))
		for _, r := range rows {
			out = append(out, planFromRow(r))
		}
		units, err = q.CountManagerUnits(ctx, tenantID)
		return err
	})
	return out, units, err
}

// CurrentSubscription is a manager subscription with its plan, or
// ErrRecordNotFound if they have none yet.
type CurrentSubscription struct {
	Subscription Subscription `json:"subscription"`
	Plan         Plan         `json:"plan"`
}

func (m BillingModel) Current(ctx context.Context, tenantID uuid.UUID) (*CurrentSubscription, error) {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	var out *CurrentSubscription
	err := m.Store.ExecTenantTx(ctx, tenantID, func(q sqlc.Querier) error {
		sub, err := q.GetSubscriptionByManager(ctx, tenantID)
		if err != nil {
			return notFound(err)
		}
		plan, err := q.GetSubscriptionPlan(ctx, sub.PlanID)
		if err != nil {
			return err
		}
		out = &CurrentSubscription{
			Subscription: Subscription{ID: sub.ID, PlanID: sub.PlanID, Status: sub.Status,
				CurrentPeriodStart: sub.CurrentPeriodStart, CurrentPeriodEnd: sub.CurrentPeriodEnd},
			Plan: planFromRow(plan),
		}
		return nil
	})
	return out, err
}

// Renewal is a pending invoice ready to be paid, and who to prompt.
type Renewal struct {
	Invoice  SubscriptionInvoice
	Plan     Plan
	FirmName string
	Phone    string
}

// StartRenewal opens a pending invoice for a plan so an STK push can be sent
// for it. planID picks the plan for a manager with no subscription yet (one
// is created past_due, active only once paid); with an existing subscription,
// planID may name any active plan to switch to it, so long as its unit cap
// covers the manager's current occupied+vacant unit count. The switch does
// not take effect immediately: the invoice remembers the chosen plan, and
// the current subscription is left unchanged until the payment webhook
// confirms it and activates that plan. Nothing is charged here.
func (m BillingModel) StartRenewal(ctx context.Context, tenantID uuid.UUID, planID *uuid.UUID) (*Renewal, error) {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	var out *Renewal
	err := m.Store.ExecTenantTx(ctx, tenantID, func(q sqlc.Querier) error {
		now := today(m.now())

		sub, err := q.GetSubscriptionByManager(ctx, tenantID)
		hasSub := err == nil
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}

		var wanted uuid.UUID
		switch {
		case planID != nil:
			wanted = *planID
		case hasSub:
			wanted = sub.PlanID
		default:
			return ErrNoPlan
		}
		row, err := q.GetSubscriptionPlan(ctx, wanted)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrNoPlan
			}
			return err
		}
		if row.ArchivedAt != nil {
			return ErrNoPlan
		}
		if row.IsTest && !m.isDevelopment() {
			return ErrPlanNotAvailable
		}
		plan := planFromRow(row)
		if !plan.Price.IsPositive() && plan.PricingType != PricingPerUnit {
			return fmt.Errorf("%w: plan %q has no price to charge", ErrInvalidInput, plan.Name)
		}
		if _, err := addInterval(now, plan.BillingInterval); err != nil {
			return err
		}
		units, err := q.CountManagerUnits(ctx, tenantID)
		if err != nil {
			return err
		}
		if planID != nil && plan.UnitCap != nil && units > *plan.UnitCap {
			return fmt.Errorf("%w: Your %d units exceed %s's limit of %d.", ErrPlanTooSmall, units, plan.Name, *plan.UnitCap)
		}
		amount := plan.PriceForUnits(units)

		if !hasSub {
			sub, err = q.CreateSubscription(ctx, sqlc.CreateSubscriptionParams{
				ManagerID: tenantID, PlanID: plan.ID, Status: SubscriptionPastDue,
				CurrentPeriodStart: now, CurrentPeriodEnd: now,
			})
			if err != nil {
				return err
			}
		}

		invoiceRow, err := q.CreateSubscriptionInvoice(ctx, sqlc.CreateSubscriptionInvoiceParams{
			ManagerID: tenantID, SubscriptionID: sub.ID, PlanID: plan.ID, Amount: amount, Period: now,
			MpesaReceipt: "PENDING-" + uuid.NewString(),
		})
		if err != nil {
			return err
		}
		contact, err := q.GetManagerBillingContact(ctx, tenantID)
		if err != nil {
			return notFound(err)
		}
		out = &Renewal{
			Invoice: invoiceFromRow(invoiceRow), Plan: plan,
			FirmName: contact.FirmName, Phone: contact.Phone,
		}
		return nil
	})
	return out, err
}

// FailInvoice marks an invoice failed when its STK push could not be sent.
func (m BillingModel) FailInvoice(ctx context.Context, tenantID, invoiceID uuid.UUID) error {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()
	return m.Store.ExecTenantTx(ctx, tenantID, func(q sqlc.Querier) error {
		_, err := q.MarkSubscriptionInvoiceFailed(ctx, sqlc.MarkSubscriptionInvoiceFailedParams{ManagerID: tenantID, ID: invoiceID})
		return err
	})
}

// Invoices pages the manager invoices, newest first.
func (m BillingModel) Invoices(ctx context.Context, tenantID uuid.UUID, filters Filters) ([]SubscriptionInvoice, Metadata, error) {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	var out []SubscriptionInvoice
	total := 0
	err := m.Store.ExecTenantTx(ctx, tenantID, func(q sqlc.Querier) error {
		rows, err := q.ListSubscriptionInvoices(ctx, sqlc.ListSubscriptionInvoicesParams{
			ManagerID: tenantID, PageLimit: int32(filters.limit()), PageOffset: int32(filters.offset()),
		})
		if err != nil {
			return err
		}
		out, total = make([]SubscriptionInvoice, 0, len(rows)), 0
		for _, r := range rows {
			total = int(r.TotalRecords)
			out = append(out, invoiceFromRow(r.SubscriptionInvoice))
		}
		return nil
	})
	return out, calculateMetadata(total, filters.Page, filters.PageSize), err
}

// InvoiceDocument is what the subscription invoice PDF needs.
type InvoiceDocument struct {
	Invoice     SubscriptionInvoice
	ManagerName string
	ManagerMail string
	PlanName    string
}

// InvoiceForPDF loads one of the manager invoices for rendering, or
// ErrRecordNotFound.
func (m BillingModel) InvoiceForPDF(ctx context.Context, tenantID, invoiceID uuid.UUID) (*InvoiceDocument, error) {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	var out *InvoiceDocument
	err := m.Store.ExecTenantTx(ctx, tenantID, func(q sqlc.Querier) error {
		row, err := q.GetSubscriptionInvoice(ctx, sqlc.GetSubscriptionInvoiceParams{ManagerID: tenantID, ID: invoiceID})
		if err != nil {
			return notFound(err)
		}
		contact, err := q.GetManagerBillingContact(ctx, tenantID)
		if err != nil {
			return notFound(err)
		}
		doc := &InvoiceDocument{Invoice: invoiceFromRow(row), ManagerName: contact.FirmName, ManagerMail: contact.Email}
		if sub, err := q.GetSubscriptionByManager(ctx, tenantID); err == nil {
			if plan, err := q.GetSubscriptionPlan(ctx, sub.PlanID); err == nil {
				doc.PlanName = plan.Name
			}
		}
		out = doc
		return nil
	})
	return out, err
}

// ExpireForDev force-sets a subscription's period to end on end, so the
// paywall closes without waiting on PayHero. cmd/devtools' dev/expire-trial
// only; never called from the API.
func (m BillingModel) ExpireForDev(ctx context.Context, tenantID, subscriptionID uuid.UUID, end time.Time) (int32, error) {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	var version int32
	err := m.Store.ExecTenantTx(ctx, tenantID, func(q sqlc.Querier) error {
		sub, err := q.GetSubscriptionByManager(ctx, tenantID)
		if err != nil {
			return notFound(err)
		}
		v, err := q.ActivateSubscription(ctx, sqlc.ActivateSubscriptionParams{
			ManagerID: tenantID, ID: subscriptionID, PlanID: sub.PlanID, CurrentPeriodStart: end, CurrentPeriodEnd: end,
		})
		version = v
		return err
	})
	return version, err
}

// SetPlanForDev puts a manager directly onto planID, active for one billing
// interval from today, with no invoice or payment. cmd/devtools'
// dev/set-plan only; never called from the API — it exists so the renew
// flow's plan-switch behavior can be tested without a real PayHero payment.
func (m BillingModel) SetPlanForDev(ctx context.Context, tenantID, planID uuid.UUID) error {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	return m.Store.ExecTenantTx(ctx, tenantID, func(q sqlc.Querier) error {
		plan, err := q.GetSubscriptionPlan(ctx, planID)
		if err != nil {
			return notFound(err)
		}
		start := today(m.now())
		end, err := addInterval(start, plan.BillingInterval)
		if err != nil {
			return err
		}

		sub, err := q.GetSubscriptionByManager(ctx, tenantID)
		if errors.Is(err, sql.ErrNoRows) {
			sub, err = q.CreateSubscription(ctx, sqlc.CreateSubscriptionParams{
				ManagerID: tenantID, PlanID: plan.ID, Status: SubscriptionPastDue,
				CurrentPeriodStart: start, CurrentPeriodEnd: start,
			})
		}
		if err != nil {
			return err
		}
		_, err = q.ActivateSubscription(ctx, sqlc.ActivateSubscriptionParams{
			ManagerID: tenantID, ID: sub.ID, PlanID: plan.ID, CurrentPeriodStart: start, CurrentPeriodEnd: end,
		})
		return err
	})
}

// DeleteForDev removes a manager's subscription (and, by cascade, its
// invoices), returning it to trial-only standing. cmd/devtools'
// dev/reset-trial only; never called from the API.
func (m BillingModel) DeleteForDev(ctx context.Context, tenantID uuid.UUID) (int64, error) {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	var n int64
	err := m.Store.ExecTenantTx(ctx, tenantID, func(q sqlc.Querier) error {
		var err error
		n, err = q.DevDeleteSubscription(ctx, tenantID)
		return err
	})
	return n, err
}

// SubscriptionResult reports what became of a subscription callback.
type SubscriptionResult struct {
	Duplicate  bool // a redelivery of a payment already applied
	InvoiceID  uuid.UUID
	ManagerID  uuid.UUID
	Paid       bool
	Failed     bool
	ActiveTill time.Time
}

// ProcessSubscriptionPayment applies a billing-channel callback: it finds the
// manager from the invoice reference we sent, then in that manager tenant
// transaction (serializable, retried) settles the invoice and flips the
// subscription to active for one more interval. A failed or cancelled push
// marks the invoice failed. A redelivery changes nothing.
func (m BillingModel) ProcessSubscriptionPayment(ctx context.Context, p *payhero.SubscriptionsWebhookPayload) (*SubscriptionResult, error) {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	invoiceID, err := uuid.Parse(p.Reference)
	if err != nil {
		return nil, fmt.Errorf("%w: %q is not an invoice reference", ErrInvalidInput, p.Reference)
	}
	managerID, ok, err := m.Resolver.SubscriptionInvoice(ctx, invoiceID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrRecordNotFound
	}

	var paid moneyfmt.Money
	if p.Success {
		if paid, err = moneyfmt.Parse(p.Amount); err != nil {
			return nil, fmt.Errorf("%w: amount %q", ErrInvalidInput, p.Amount)
		}
	}

	var result SubscriptionResult
	err = m.Store.ExecTenantTx(ctx, managerID, func(q sqlc.Querier) error {
		result = SubscriptionResult{InvoiceID: invoiceID, ManagerID: managerID}

		invoice, err := q.GetSubscriptionInvoice(ctx, sqlc.GetSubscriptionInvoiceParams{ManagerID: managerID, ID: invoiceID})
		if err != nil {
			return notFound(err)
		}

		if !p.Success {
			n, err := q.MarkSubscriptionInvoiceFailed(ctx, sqlc.MarkSubscriptionInvoiceFailedParams{ManagerID: managerID, ID: invoiceID})
			result.Failed = n > 0
			return err
		}
		if invoice.Status == InvoicePaid {
			result.Duplicate = true
			return nil
		}
		if paid.Cmp(invoice.Amount) < 0 {
			return fmt.Errorf("%w: paid %s of %s", ErrUnderpaid, paid, invoice.Amount)
		}

		now := m.now()
		n, err := q.MarkSubscriptionInvoicePaid(ctx, sqlc.MarkSubscriptionInvoicePaidParams{
			ManagerID: managerID, ID: invoiceID, MpesaReceipt: p.MpesaReceipt, PaidAt: &now,
		})
		if err != nil {
			if isUniqueViolation(err, "subscription_invoices_mpesa_receipt_key") {
				// The failed statement aborted the transaction, so it must
				// roll back; the caller turns this into a duplicate.
				return errReceiptReused
			}
			return err
		}
		if n == 0 {
			result.Duplicate = true
			return nil
		}

		sub, err := q.GetSubscriptionByManager(ctx, managerID)
		if err != nil {
			return notFound(err)
		}
		// The invoice's own plan_id is what the manager chose at renewal
		// time, which may differ from the subscription's plan if they
		// switched: activating it here is what makes the switch take effect.
		plan, err := q.GetSubscriptionPlan(ctx, invoice.PlanID)
		if err != nil {
			return err
		}
		if plan.IsTest && !m.isDevelopment() {
			// The environment flipped to non-development between renewal and
			// this callback: refuse to activate a TEST plan. The invoice
			// stays paid but the subscription is left alone; an admin must
			// resolve this by hand.
			return fmt.Errorf("%w: plan %q", ErrPlanNotAvailable, plan.Name)
		}
		// Extend from the end of a still-running period, otherwise from today.
		start := today(now)
		if sub.Status == SubscriptionActive && sub.PlanID == plan.ID && sub.CurrentPeriodEnd.After(start) {
			start = sub.CurrentPeriodEnd
		}
		end, err := addInterval(start, plan.BillingInterval)
		if err != nil {
			return err
		}
		if _, err := q.ActivateSubscription(ctx, sqlc.ActivateSubscriptionParams{
			ManagerID: managerID, ID: sub.ID, PlanID: plan.ID, CurrentPeriodStart: start, CurrentPeriodEnd: end,
		}); err != nil {
			return err
		}
		result.Paid, result.ActiveTill = true, end
		return nil
	})
	if errors.Is(err, errReceiptReused) {
		// This receipt already settled another invoice: a redelivery.
		return &SubscriptionResult{Duplicate: true, InvoiceID: invoiceID, ManagerID: managerID}, nil
	}
	if err != nil {
		return nil, err
	}
	return &result, nil
}
