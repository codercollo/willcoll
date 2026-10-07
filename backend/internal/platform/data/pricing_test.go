package data

import (
	"errors"
	"testing"
	"time"

	"github.com/codercollo/willcoll/backend/pkg/moneyfmt"
	"github.com/google/uuid"
)

func money(t *testing.T, s string) moneyfmt.Money {
	t.Helper()
	m, err := moneyfmt.Parse(s)
	if err != nil {
		t.Fatalf("moneyfmt.Parse(%q): %v", s, err)
	}
	return m
}

func TestPriceFlatOrUsage_FlatPlanNeedsNoUsage(t *testing.T) {
	plan := Plan{PricingType: PricingFlat, Price: money(t, "1500.00")}

	// No usage row at all — a flat plan must still price fine.
	got, err := PriceFlatOrUsage(plan, nil, time.Now())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Amount.Cmp(plan.Price) != 0 {
		t.Errorf("Amount = %s, want %s", got.Amount, plan.Price)
	}
	if got.Stale {
		t.Errorf("Stale = true for a flat plan, want false")
	}
}

func TestPriceFlatOrUsage_PerUnitWithNoUsageIsRefused(t *testing.T) {
	plan := Plan{
		PricingType: PricingPerUnit,
		PerUnitPrice: func() *moneyfmt.Money {
			m := money(t, "45.00")
			return &m
		}(),
		MinPrice: func() *moneyfmt.Money {
			m := money(t, "7500.00")
			return &m
		}(),
	}

	_, err := PriceFlatOrUsage(plan, nil, time.Now())
	if !errors.Is(err, ErrUsageUnavailable) {
		t.Fatalf("err = %v, want ErrUsageUnavailable", err)
	}
}

func TestPriceFlatOrUsage_PerUnitPricesFromUsage(t *testing.T) {
	perUnit := money(t, "45.00")
	minPrice := money(t, "7500.00")
	plan := Plan{PricingType: PricingPerUnit, PerUnitPrice: &perUnit, MinPrice: &minPrice}

	now := time.Now()
	usage := &ModuleUsage{
		ManagerID: uuid.New(), Product: ProductRentals,
		BillableCount: 200, ReportedAt: now,
	}

	got, err := PriceFlatOrUsage(plan, usage, now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := plan.PriceForUnits(200) // 200 * 45 = 9000, above the 7500 minimum
	if got.Amount.Cmp(want) != 0 {
		t.Errorf("Amount = %s, want %s", got.Amount, want)
	}
	if got.Stale {
		t.Errorf("Stale = true for a fresh usage row, want false")
	}
}

func TestPriceFlatOrUsage_PerUnitBelowMinimumUsesMinimum(t *testing.T) {
	perUnit := money(t, "45.00")
	minPrice := money(t, "7500.00")
	plan := Plan{PricingType: PricingPerUnit, PerUnitPrice: &perUnit, MinPrice: &minPrice}

	now := time.Now()
	usage := &ModuleUsage{ManagerID: uuid.New(), Product: ProductRentals, BillableCount: 5, ReportedAt: now}

	got, err := PriceFlatOrUsage(plan, usage, now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// 5 * 45 = 225, well under the 7500 minimum.
	if got.Amount.Cmp(minPrice) != 0 {
		t.Errorf("Amount = %s, want the plan minimum %s", got.Amount, minPrice)
	}
}

func TestPriceFlatOrUsage_StaleUsageStillPricesButFlags(t *testing.T) {
	perUnit := money(t, "45.00")
	minPrice := money(t, "7500.00")
	plan := Plan{PricingType: PricingPerUnit, PerUnitPrice: &perUnit, MinPrice: &minPrice}

	now := time.Now()
	staleUsage := &ModuleUsage{
		ManagerID: uuid.New(), Product: ProductRentals,
		BillableCount: 300, ReportedAt: now.Add(-72 * time.Hour), // older than UsageStaleAfter (48h)
	}

	got, err := PriceFlatOrUsage(plan, staleUsage, now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := plan.PriceForUnits(300)
	if got.Amount.Cmp(want) != 0 {
		t.Errorf("Amount = %s, want %s (a stale row is still priced from)", got.Amount, want)
	}
	if !got.Stale {
		t.Errorf("Stale = false for a row older than UsageStaleAfter, want true")
	}
}

func TestPriceFlatOrUsage_UsageJustUnderStaleThresholdIsNotFlagged(t *testing.T) {
	perUnit := money(t, "45.00")
	minPrice := money(t, "7500.00")
	plan := Plan{PricingType: PricingPerUnit, PerUnitPrice: &perUnit, MinPrice: &minPrice}

	now := time.Now()
	usage := &ModuleUsage{
		ManagerID: uuid.New(), Product: ProductRentals,
		BillableCount: 300, ReportedAt: now.Add(-UsageStaleAfter + time.Minute),
	}

	got, err := PriceFlatOrUsage(plan, usage, now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Stale {
		t.Errorf("Stale = true for a row just under the threshold, want false")
	}
}
