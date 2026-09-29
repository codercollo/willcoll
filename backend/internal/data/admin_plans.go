package data

import (
	"context"
	"database/sql"
	"strings"

	"github.com/codercollo/willcoll/backend/internal/db/sqlc"
	"github.com/codercollo/willcoll/backend/internal/validator"
	"github.com/codercollo/willcoll/backend/pkg/moneyfmt"
	"github.com/google/uuid"
)

// billingIntervals are the intervals a plan may bill on; addInterval knows
// each of them.
var billingIntervals = []string{"weekly", "monthly", "quarterly", "yearly"}

// PlanInput is a plan as the Super Admin writes it.
type PlanInput struct {
	Name            string
	Price           moneyfmt.Money
	BillingInterval string
	UnitCap         *int32
	IsTest          bool
	PricingType     string
	PerUnitPrice    *moneyfmt.Money
	MinPrice        *moneyfmt.Money
	SortOrder       int32
}

// AdminPlan is a plan with how many subscriptions use it.
type AdminPlan struct {
	Plan
	Subscribers int64 `json:"subscribers"`
}

// pricingTypes are the ways a plan may be priced; addInterval-style billing
// still applies to both.
var pricingTypes = []string{PricingFlat, PricingPerUnit}

// ValidatePlan checks a plan. Money fields must be a whole number of
// shillings: subscriptions are paid by M-Pesa STK push, which cannot carry
// cents. A flat plan is priced from Price alone; a per_unit plan from
// PerUnitPrice and MinPrice, and Price is ignored for it.
func ValidatePlan(v *validator.Validator, p *PlanInput) {
	p.Name = strings.TrimSpace(p.Name)
	p.BillingInterval = strings.ToLower(strings.TrimSpace(p.BillingInterval))
	if p.PricingType == "" {
		p.PricingType = PricingFlat
	}
	p.PricingType = strings.ToLower(strings.TrimSpace(p.PricingType))

	v.Check(p.Name != "", "name", "must be provided")
	v.Check(len(p.Name) <= 100, "name", "must not be more than 100 bytes long")
	v.Check(validator.PermittedValue(p.BillingInterval, billingIntervals...), "billing_interval", "must be one of weekly, monthly, quarterly, yearly")
	v.Check(validator.PermittedValue(p.PricingType, pricingTypes...), "pricing_type", "must be one of flat, per_unit")
	if p.UnitCap != nil {
		v.Check(*p.UnitCap > 0, "unit_cap", "must be greater than zero, or omitted for no cap")
	}

	switch p.PricingType {
	case PricingPerUnit:
		v.Check(p.PerUnitPrice != nil && p.PerUnitPrice.IsPositive(), "per_unit_price", "must be greater than zero")
		v.Check(p.MinPrice != nil && p.MinPrice.IsPositive(), "min_price", "must be greater than zero")
		if p.PerUnitPrice != nil {
			v.Check(p.PerUnitPrice.Cents()%100 == 0, "per_unit_price", "must be a whole number of shillings")
		}
		if p.MinPrice != nil {
			v.Check(p.MinPrice.Cents()%100 == 0, "min_price", "must be a whole number of shillings")
		}
		p.Price = moneyfmt.Money{}
	default: // flat
		v.Check(p.Price.IsPositive(), "price", "must be greater than zero")
		v.Check(p.Price.Cents()%100 == 0, "price", "must be a whole number of shillings")
		p.PerUnitPrice, p.MinPrice = nil, nil
	}
}

func unitCapParam(c *int32) sql.NullInt32 {
	if c == nil {
		return sql.NullInt32{}
	}
	return sql.NullInt32{Int32: *c, Valid: true}
}

// ListPlans returns every plan with its subscriber count, active and
// archived alike; the admin UI filters by tab.
func (m PlatformModel) ListPlans(ctx context.Context) ([]AdminPlan, error) {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	rows, err := m.Store.AdminListSubscriptionPlans(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]AdminPlan, 0, len(rows))
	for _, r := range rows {
		out = append(out, AdminPlan{
			Plan: planFromRow(sqlc.SubscriptionPlan{
				ID: r.ID, Name: r.Name, Price: r.Price, BillingInterval: r.BillingInterval, UnitCap: r.UnitCap,
				IsTest: r.IsTest, ArchivedAt: r.ArchivedAt, PricingType: r.PricingType,
				PerUnitPrice: r.PerUnitPrice, MinPrice: r.MinPrice, SortOrder: r.SortOrder,
			}),
			Subscribers: r.Subscribers,
		})
	}
	return out, nil
}

// CreatePlan adds a plan. A test plan (IsTest) may only be created in
// development — ErrPlanNotAvailable outside it, checked here so the
// guard holds regardless of caller.
func (m PlatformModel) CreatePlan(ctx context.Context, in PlanInput, isDevelopment bool) (*Plan, error) {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	if in.IsTest && !isDevelopment {
		return nil, ErrPlanNotAvailable
	}
	if in.PricingType == "" {
		in.PricingType = PricingFlat
	}
	row, err := m.Store.AdminCreateSubscriptionPlan(ctx, sqlc.AdminCreateSubscriptionPlanParams{
		Name: in.Name, Price: in.Price, BillingInterval: in.BillingInterval, UnitCap: unitCapParam(in.UnitCap),
		IsTest: in.IsTest, PricingType: in.PricingType, PerUnitPrice: in.PerUnitPrice, MinPrice: in.MinPrice, SortOrder: in.SortOrder,
	})
	if err != nil {
		return nil, err
	}
	p := planFromRow(row)
	return &p, nil
}

// UpdatePlan changes a plan, or returns ErrRecordNotFound. A new price
// applies to invoices raised from then on. Setting IsTest true is only
// allowed in development.
func (m PlatformModel) UpdatePlan(ctx context.Context, id uuid.UUID, in PlanInput, isDevelopment bool) (*Plan, error) {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	if in.IsTest && !isDevelopment {
		return nil, ErrPlanNotAvailable
	}
	if in.PricingType == "" {
		in.PricingType = PricingFlat
	}
	row, err := m.Store.AdminUpdateSubscriptionPlan(ctx, sqlc.AdminUpdateSubscriptionPlanParams{
		ID: id, Name: in.Name, Price: in.Price, BillingInterval: in.BillingInterval, UnitCap: unitCapParam(in.UnitCap),
		IsTest: in.IsTest, PricingType: in.PricingType, PerUnitPrice: in.PerUnitPrice, MinPrice: in.MinPrice, SortOrder: in.SortOrder,
	})
	if err != nil {
		return nil, notFound(err)
	}
	p := planFromRow(row)
	return &p, nil
}

// ArchivePlan hides a plan from managers and blocks renewals onto it.
// Existing subscribers keep their current period; history is untouched.
func (m PlatformModel) ArchivePlan(ctx context.Context, id uuid.UUID) (*Plan, error) {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	row, err := m.Store.AdminArchiveSubscriptionPlan(ctx, id)
	if err != nil {
		return nil, notFound(err)
	}
	p := planFromRow(row)
	return &p, nil
}

// RestorePlan un-archives a plan.
func (m PlatformModel) RestorePlan(ctx context.Context, id uuid.UUID) (*Plan, error) {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	row, err := m.Store.AdminRestoreSubscriptionPlan(ctx, id)
	if err != nil {
		return nil, notFound(err)
	}
	p := planFromRow(row)
	return &p, nil
}

// GetPlan returns one plan, or ErrRecordNotFound.
func (m PlatformModel) GetPlan(ctx context.Context, id uuid.UUID) (*Plan, error) {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	row, err := m.Store.GetSubscriptionPlan(ctx, id)
	if err != nil {
		return nil, notFound(err)
	}
	p := planFromRow(row)
	return &p, nil
}
