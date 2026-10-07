package data

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/codercollo/willcoll/backend/internal/platform/db"
	"github.com/codercollo/willcoll/backend/internal/platform/db/sqlc"
	"github.com/codercollo/willcoll/backend/internal/shared/validator"
	"github.com/codercollo/willcoll/backend/pkg/moneyfmt"
	"github.com/google/uuid"
)

// Plan pricing types.
const (
	PricingFlat    = "flat"
	PricingPerUnit = "per_unit"
)

// Products a plan (or a subscription) can belong to (system-design.txt A.3).
const (
	ProductRentals = "rentals"
	ProductEstates = "estates"
	ProductWaste   = "waste"
)

// ErrPlanNotAvailable is returned when a TEST plan is created, edited, or
// (once StartRenewal moves here — build-phases.txt 3c-iv) renewed onto or
// activated, outside development.
var ErrPlanNotAvailable = errors.New("plan not available")

// Plan is a subscription plan. A flat plan charges Price; a per_unit plan
// charges max(PerUnitPrice * billable count, MinPrice) — priced from
// module_usage once 3c-iv moves StartRenewal here, not queried live.
type Plan struct {
	ID              uuid.UUID       `json:"id"`
	Product         string          `json:"product"`
	Name            string          `json:"name"`
	Price           moneyfmt.Money  `json:"price"`
	BillingInterval string          `json:"billing_interval"`
	UnitCap         *int32          `json:"unit_cap,omitempty"`
	IncludedSeats   int32           `json:"included_seats"`
	Features        []string        `json:"features"`
	IsTest          bool            `json:"is_test"`
	Archived        bool            `json:"archived"`
	PricingType     string          `json:"pricing_type"`
	PerUnitPrice    *moneyfmt.Money `json:"per_unit_price,omitempty"`
	MinPrice        *moneyfmt.Money `json:"min_price,omitempty"`
	SortOrder       int32           `json:"sort_order"`
}

// PriceForUnits is what a plan costs for a billable count of units/seats.
// Flat plans ignore it; a per_unit plan is max(PerUnitPrice*count, MinPrice).
func (p Plan) PriceForUnits(count int32) moneyfmt.Money {
	if p.PricingType != PricingPerUnit || p.PerUnitPrice == nil {
		return p.Price
	}
	amount := moneyfmt.FromCents(p.PerUnitPrice.Cents() * int64(count))
	if p.MinPrice != nil && amount.Cmp(*p.MinPrice) < 0 {
		return *p.MinPrice
	}
	return amount
}

// billingIntervals are the intervals a plan may bill on.
var billingIntervals = []string{"weekly", "monthly", "quarterly", "yearly"}

// products are the values Product/PlanInput.Product may take.
var products = []string{ProductRentals, ProductEstates, ProductWaste}

// pricingTypes are the ways a plan may be priced.
var pricingTypes = []string{PricingFlat, PricingPerUnit}

// PlanInput is a plan as the Super Admin writes it.
type PlanInput struct {
	Product         string
	Name            string
	Price           moneyfmt.Money
	BillingInterval string
	UnitCap         *int32
	IncludedSeats   int32
	Features        []string
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

// ValidatePlan checks a plan. Money fields must be a whole number of
// shillings: subscriptions are paid by M-Pesa STK push, which cannot carry
// cents. A flat plan is priced from Price alone; a per_unit plan from
// PerUnitPrice and MinPrice, and Price is ignored for it.
func ValidatePlan(v *validator.Validator, p *PlanInput) {
	p.Product = strings.ToLower(strings.TrimSpace(p.Product))
	p.Name = strings.TrimSpace(p.Name)
	p.BillingInterval = strings.ToLower(strings.TrimSpace(p.BillingInterval))
	if p.PricingType == "" {
		p.PricingType = PricingFlat
	}
	p.PricingType = strings.ToLower(strings.TrimSpace(p.PricingType))
	if p.IncludedSeats <= 0 {
		p.IncludedSeats = 1
	}
	if p.Features == nil {
		p.Features = []string{}
	}

	v.Check(validator.PermittedValue(p.Product, products...), "product", "must be one of rentals, estates, waste")
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

func planFromRow(r sqlc.SubscriptionPlan) Plan {
	p := Plan{
		ID: r.ID, Product: r.Product, Name: r.Name, Price: r.Price, BillingInterval: r.BillingInterval,
		IncludedSeats: r.IncludedSeats, Features: []string(r.Features),
		IsTest: r.IsTest, Archived: r.ArchivedAt != nil, PricingType: r.PricingType,
		PerUnitPrice: r.PerUnitPrice, MinPrice: r.MinPrice, SortOrder: r.SortOrder,
	}
	if r.UnitCap.Valid {
		v := r.UnitCap.Int32
		p.UnitCap = &v
	}
	if p.Features == nil {
		p.Features = []string{}
	}
	return p
}

// PlanModel is admin-only plan CRUD. subscription_plans revokes
// INSERT/UPDATE/DELETE from willcoll_platform_app (migration 000013), so
// Store here is always the willcoll_platform_admin pool.
type PlanModel struct {
	Store db.Store

	// Timeout bounds each method call; zero means DefaultQueryTimeout.
	Timeout time.Duration
}

// List returns every plan with its subscriber count, active and archived
// alike, across every product; callers filter by product/tab.
func (m PlanModel) List(ctx context.Context) ([]AdminPlan, error) {
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
				ID: r.ID, Product: r.Product, Name: r.Name, Price: r.Price, BillingInterval: r.BillingInterval,
				UnitCap: r.UnitCap, IncludedSeats: r.IncludedSeats, Features: r.Features,
				IsTest: r.IsTest, ArchivedAt: r.ArchivedAt, PricingType: r.PricingType,
				PerUnitPrice: r.PerUnitPrice, MinPrice: r.MinPrice, SortOrder: r.SortOrder,
			}),
			Subscribers: r.Subscribers,
		})
	}
	return out, nil
}

// Create adds a plan. A test plan (IsTest) may only be created in
// development — ErrPlanNotAvailable outside it, checked here so the
// guard holds regardless of caller.
func (m PlanModel) Create(ctx context.Context, in PlanInput, isDevelopment bool) (*Plan, error) {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	if in.IsTest && !isDevelopment {
		return nil, ErrPlanNotAvailable
	}
	if in.PricingType == "" {
		in.PricingType = PricingFlat
	}
	row, err := m.Store.AdminCreateSubscriptionPlan(ctx, sqlc.AdminCreateSubscriptionPlanParams{
		Product: in.Product, Name: in.Name, Price: in.Price, BillingInterval: in.BillingInterval,
		UnitCap: unitCapParam(in.UnitCap), IncludedSeats: in.IncludedSeats, Features: in.Features,
		IsTest: in.IsTest, PricingType: in.PricingType, PerUnitPrice: in.PerUnitPrice, MinPrice: in.MinPrice, SortOrder: in.SortOrder,
	})
	if err != nil {
		return nil, err
	}
	p := planFromRow(row)
	return &p, nil
}

// Update changes a plan, or returns ErrRecordNotFound. A new price applies
// to invoices raised from then on. Setting IsTest true is only allowed in
// development. Product is fixed at creation and not editable here.
func (m PlanModel) Update(ctx context.Context, id uuid.UUID, in PlanInput, isDevelopment bool) (*Plan, error) {
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
		IncludedSeats: in.IncludedSeats, Features: in.Features,
		IsTest: in.IsTest, PricingType: in.PricingType, PerUnitPrice: in.PerUnitPrice, MinPrice: in.MinPrice, SortOrder: in.SortOrder,
	})
	if err != nil {
		return nil, notFound(err)
	}
	p := planFromRow(row)
	return &p, nil
}

// Archive hides a plan from managers and blocks renewals onto it. Existing
// subscribers keep their current period; history is untouched.
func (m PlanModel) Archive(ctx context.Context, id uuid.UUID) (*Plan, error) {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	row, err := m.Store.AdminArchiveSubscriptionPlan(ctx, id)
	if err != nil {
		return nil, notFound(err)
	}
	p := planFromRow(row)
	return &p, nil
}

// Restore un-archives a plan.
func (m PlanModel) Restore(ctx context.Context, id uuid.UUID) (*Plan, error) {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	row, err := m.Store.AdminRestoreSubscriptionPlan(ctx, id)
	if err != nil {
		return nil, notFound(err)
	}
	p := planFromRow(row)
	return &p, nil
}

// Get returns one plan, or ErrRecordNotFound.
func (m PlanModel) Get(ctx context.Context, id uuid.UUID) (*Plan, error) {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	row, err := m.Store.GetSubscriptionPlan(ctx, id)
	if err != nil {
		return nil, notFound(err)
	}
	p := planFromRow(row)
	return &p, nil
}
