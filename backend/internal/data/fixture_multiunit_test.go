package data

import (
	"context"
	"database/sql"
	"strconv"
	"testing"
	"time"

	"github.com/codercollo/willcoll/backend/pkg/moneyfmt"
	"github.com/google/uuid"
)

// fortyEightUnitFixture is the reusable "big property" test fixture: 48
// units covering co-tenants (OR), a C/O business tenant, vacant units,
// stacked payments, and garbage billed for some units but not others. Built
// once and shared by the schedule/bills/receipts golden tests, per the
// request to reuse the same fixture across all of them.
type fortyEightUnitFixture struct {
	Tenant   uuid.UUID
	Property *Property
	Units    []*Unit
	Leases   map[uuid.UUID]*Lease // by unit ID; absent for vacant units
	Period   moneyfmt.Period
}

func buildFortyEightUnitFixture(t *testing.T, conn *sql.DB, models Models) *fortyEightUnitFixture {
	t.Helper()
	ctx := context.Background()
	tenant, landlord := seedTenant(t, conn)

	property := &Property{
		LandlordID: landlord, Name: "The Rundas Arcade", Location: "Kasarani", Slug: "rundas-multi-" + uuid.NewString()[:8],
		WaterRatePerUnit: biMoney(t, "150"), ManagementFeePercent: 5, GarbageEnabled: true, GarbageFee: biMoney(t, "300"),
	}
	if err := models.Properties.Insert(ctx, tenant, property); err != nil {
		t.Fatal(err)
	}
	property.UndergroundCapacityUnits = ptrMoney(biMoney(t, "20"))
	property.RooftopCapacityUnits = ptrMoney(biMoney(t, "20"))
	if err := models.Properties.Update(ctx, property); err != nil {
		t.Fatalf("set storage capacity: %v", err)
	}

	period := biPeriod(t, "2026-08")
	leaseStart := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	blocks := []string{"1", "2", "3", "4", "5", "6", "7", "8"}
	letters := []string{"A", "B", "C", "D", "E", "F"}

	fx := &fortyEightUnitFixture{Tenant: tenant, Property: property, Leases: map[uuid.UUID]*Lease{}, Period: period}

	vacant := map[int]bool{2: true, 15: true, 30: true}
	i := 0
	for _, b := range blocks {
		for _, l := range letters {
			code := b + l
			unit := &Unit{PropertyID: property.ID, UnitCode: code, Status: "vacant"}
			if err := models.Units.Insert(ctx, tenant, unit); err != nil {
				t.Fatalf("unit %s: %v", code, err)
			}
			fx.Units = append(fx.Units, unit)

			if vacant[i] {
				i++
				continue
			}

			rent := biMoney(t, "12000")
			tenantName := "TENANT " + code
			garbageBilled := i%4 == 0
			var payers []LeasePayer
			switch i {
			case 0: // co-tenants, joined with "OR"
				tenantName = "JANE WAMBUI"
				payers = []LeasePayer{{Name: "PETER KAMAU"}}
			case 1: // business tenant, "C/O"
				tenantName = "MERCY KANANA C/O KANANA HARDWARE"
			}

			lease := &Lease{UnitID: unit.ID, TenantName: tenantName, PrimaryPhone: "+254700000000",
				RentAmount: rent, StartDate: leaseStart, Status: "active", GarbageBilled: garbageBilled, Payers: payers}
			if err := models.Leases.Insert(ctx, tenant, tenant, lease); err != nil {
				t.Fatalf("lease %s: %v", code, err)
			}
			fx.Leases[unit.ID] = lease

			rcCharge(t, conn, tenant, unit.ID, rent, period.FirstDay(), time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC))
			if i == 3 { // stacked payments: two part-payments, not one
				rcPay(t, conn, tenant, unit.ID, biMoney(t, "7000"), time.Date(2026, 8, 3, 0, 0, 0, 0, time.UTC))
				rcPay(t, conn, tenant, unit.ID, biMoney(t, "5000"), time.Date(2026, 8, 17, 0, 0, 0, 0, time.UTC))
			} else {
				rcPay(t, conn, tenant, unit.ID, rent, time.Date(2026, 8, int(5+i%20), 0, 0, 0, 0, time.UTC))
			}

			if err := models.Water.SaveReadings(ctx, tenant, tenant, property.ID, period, []WaterReadingInput{
				{UnitID: unit.ID, CurrentReading: biMoney(t, "10")},
			}); err != nil {
				t.Fatalf("water reading %s: %v", code, err)
			}
			i++
		}
	}

	if _, err := models.Water.Generate(ctx, tenant, tenant, property.ID, period); err != nil {
		t.Fatalf("water run: %v", err)
	}
	if _, err := models.Garbage.Generate(ctx, tenant, tenant, property.ID, period); err != nil {
		t.Fatalf("garbage run: %v", err)
	}

	// Everyone who's billed pays their water and (if billed) garbage in full.
	for unitID, lease := range fx.Leases {
		rcPayLedger(t, conn, tenant, unitID, LedgerTypeWater, biMoney(t, "1500"), time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC))
		if lease.GarbageBilled {
			rcPayLedger(t, conn, tenant, unitID, LedgerTypeGarbage, biMoney(t, "300"), time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC))
		}
	}

	occupied := len(fx.Leases)
	zeroReading := biMoney(t, "0")
	if err := models.Reports.SetPlotMeterReading(ctx, tenant, tenant, property.ID, period, biMoney(t, strconv.Itoa(occupied*10+40)), &zeroReading, time.Time{}); err != nil {
		t.Fatalf("plot meter reading: %v", err)
	}

	return fx
}
