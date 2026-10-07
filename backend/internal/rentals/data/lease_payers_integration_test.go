package data

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

// TestLeasePayersIntegration covers co-payers end to end: they are saved with
// the lease, bounded, isolated per manager, and they take effect where the
// spec needs them: their phone may ask for a pay-page code, and their name is
// an "OR" name on the payments schedule.
func TestLeasePayersIntegration(t *testing.T) {
	models, conn, _ := biModels(t)
	ctx := context.Background()

	tenant, landlord := seedTenant(t, conn)
	other, _ := seedTenant(t, conn)

	property := &Property{LandlordID: landlord, Name: "Runda Arcade", Location: "Runda", Slug: "ra-" + uuid.NewString()[:8]}
	if err := models.Properties.Insert(ctx, tenant, property); err != nil {
		t.Fatal(err)
	}
	unit := &Unit{PropertyID: property.ID, UnitCode: "A1", Status: "vacant"}
	if err := models.Units.Insert(ctx, tenant, unit); err != nil {
		t.Fatal(err)
	}

	ptr := func(s string) *string { return &s }
	lease := &Lease{UnitID: unit.ID, TenantName: "JOHN KAMAU", PrimaryPhone: "+254722000001", RentAmount: biMoney(t, "5000"),
		StartDate: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), Status: "active",
		Payers: []LeasePayer{{Name: "MARY WANJIKU", Phone: ptr("+254733000001")}}}
	if err := models.Leases.Insert(ctx, tenant, tenant, lease); err != nil {
		t.Fatal(err)
	}
	if len(lease.Payers) != 1 || lease.Payers[0].ID == uuid.Nil {
		t.Fatalf("payers saved with the lease = %+v", lease.Payers)
	}

	got, err := models.Leases.Get(ctx, tenant, lease.ID)
	if err != nil || len(got.Payers) != 1 || got.Payers[0].Name != "MARY WANJIKU" {
		t.Fatalf("Get = %+v, %v", got, err)
	}

	// --- Rules.
	if err := models.Leases.AddPayer(ctx, tenant, lease.ID, &LeasePayer{Name: "X", Phone: ptr("+254722000001")}); !errors.Is(err, ErrDuplicatePayer) {
		t.Errorf("the tenant's own phone as a co-payer: err = %v, want ErrDuplicatePayer", err)
	}
	if err := models.Leases.AddPayer(ctx, tenant, lease.ID, &LeasePayer{Name: "X", Phone: ptr("+254 733 000 001")}); !errors.Is(err, ErrDuplicatePayer) {
		t.Errorf("a repeated co-payer phone (other spelling): err = %v, want ErrDuplicatePayer", err)
	}
	if err := models.Leases.AddPayer(ctx, other, lease.ID, &LeasePayer{Name: "X"}); !errors.Is(err, ErrLeaseNotFound) {
		t.Errorf("another manager adding a co-payer: err = %v, want ErrLeaseNotFound", err)
	}

	peter := &LeasePayer{Name: "PETER OMONDI"} // a co-payer with no phone is fine
	if err := models.Leases.AddPayer(ctx, tenant, lease.ID, peter); err != nil || peter.ID == uuid.Nil {
		t.Fatalf("AddPayer: %v", err)
	}
	for i := 0; i < 3; i++ { // now at 5
		p := &LeasePayer{Name: "EXTRA", Phone: ptr("+25470000000" + string(rune('1'+i)))}
		if err := models.Leases.AddPayer(ctx, tenant, lease.ID, p); err != nil {
			t.Fatalf("AddPayer %d: %v", i, err)
		}
	}
	if err := models.Leases.AddPayer(ctx, tenant, lease.ID, &LeasePayer{Name: "ONE TOO MANY"}); !errors.Is(err, ErrTooManyPayers) {
		t.Errorf("a sixth co-payer: err = %v, want ErrTooManyPayers", err)
	}

	// --- They take effect: the co-payer phone may request a pay-page code, a
	// stranger's may not.
	target := PayTarget{TenantID: tenant, PropertyID: property.ID, UnitID: unit.ID}
	hash := func(u uuid.UUID, phone, code string) string { return u.String() + phone + code }
	if _, issued, err := models.PayAccess.IssueOTP(ctx, target, "0733 000 001", hash, time.Minute); err != nil || !issued {
		t.Errorf("co-payer phone could not request a code: issued=%v err=%v", issued, err)
	}
	if _, issued, _ := models.PayAccess.IssueOTP(ctx, target, "0799999999", hash, time.Minute); issued {
		t.Error("a stranger's phone was issued a code")
	}

	// --- ...and the name is an "OR" name on the schedule.
	rep, err := models.Reports.Preview(ctx, tenant, property.ID, biPeriod(t, "2026-09"))
	if err != nil || len(rep.Rows) != 1 || len(rep.Rows[0].Tenants) < 3 || rep.Rows[0].Tenants[0] != "JOHN KAMAU" {
		t.Errorf("schedule tenants = %+v, %v; want the tenant then the co-payers", rep.Rows, err)
	}

	// --- Removal.
	if err := models.Leases.RemovePayer(ctx, tenant, lease.ID, peter.ID); err != nil {
		t.Fatal(err)
	}
	if err := models.Leases.RemovePayer(ctx, tenant, lease.ID, peter.ID); !errors.Is(err, ErrRecordNotFound) {
		t.Errorf("removing twice: err = %v, want ErrRecordNotFound", err)
	}
	if err := models.Leases.RemovePayer(ctx, other, lease.ID, got.Payers[0].ID); !errors.Is(err, ErrRecordNotFound) {
		t.Errorf("another manager removing a co-payer: err = %v, want ErrRecordNotFound", err)
	}

	// --- History: terminate, then a new lease; newest first, each with its own payers.
	got, _ = models.Leases.Get(ctx, tenant, lease.ID)
	end := time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC)
	got.EndDate, got.Status = &end, LeaseStatusTerminated
	if err := models.Leases.Update(ctx, got); err != nil {
		t.Fatal(err)
	}
	next := &Lease{UnitID: unit.ID, TenantName: "NEW TENANT", PrimaryPhone: "+254744000001", RentAmount: biMoney(t, "6000"),
		StartDate: time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC), Status: "active"}
	if err := models.Leases.Insert(ctx, tenant, tenant, next); err != nil {
		t.Fatal(err)
	}
	history, err := models.Leases.ListForUnit(ctx, tenant, unit.ID)
	if err != nil || len(history) != 2 || history[0].TenantName != "NEW TENANT" || history[1].TenantName != "JOHN KAMAU" {
		t.Fatalf("history = %+v, %v", history, err)
	}
	if len(history[0].Payers) != 0 || len(history[1].Payers) != 4 {
		t.Errorf("payers per lease = %d and %d, want 0 and 4", len(history[0].Payers), len(history[1].Payers))
	}
	if _, err := models.Leases.ListForUnit(ctx, other, unit.ID); !errors.Is(err, ErrRecordNotFound) {
		t.Errorf("another manager reading the history: err = %v", err)
	}
	// A terminated lease's co-payers no longer count as payers for the unit.
	if _, issued, _ := models.PayAccess.IssueOTP(ctx, target, "0733000001", hash, time.Minute); issued {
		t.Error("a co-payer of a terminated lease can still request a code")
	}
}
