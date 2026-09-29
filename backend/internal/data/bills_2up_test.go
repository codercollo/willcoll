package data

import (
	"context"
	"strings"
	"testing"

	"github.com/codercollo/willcoll/backend/internal/db"
	"github.com/codercollo/willcoll/backend/internal/pdf"
	"github.com/google/uuid"
)

// TestWaterAndGarbageBills2Up renders the 48-unit fixture's water and
// garbage bills in the 2-up A4 layout (bills are never gated on
// confirmation — they render straight from the run's invoice lines) and
// saves both for a human to eyeball.
func TestWaterAndGarbageBills2Up(t *testing.T) {
	conn := openTestDB(t)
	models := NewModelsFromStore(db.NewStore(conn), integrationTimeout)
	ctx := context.Background()

	fx := buildFortyEightUnitFixture(t, conn, models)

	waterInvoices, err := models.Water.Invoices(ctx, fx.Tenant, fx.Property.ID, fx.Period, nil)
	if err != nil {
		t.Fatalf("Water.Invoices: %v", err)
	}
	if len(waterInvoices) == 0 {
		t.Fatal("expected water invoices")
	}
	waterPDF, err := pdf.BuildWaterInvoices2Up(waterInvoices)
	if err != nil {
		t.Fatalf("BuildWaterInvoices2Up: %v", err)
	}
	waterText := extractPDFText(t, waterPDF)
	for _, want := range []string{"WATER BILL", "Current Meter Reading", "Previous Meter Reading", "TOTAL AMOUNT DUE TO DATE"} {
		if !strings.Contains(waterText, want) {
			t.Errorf("water bill missing %q", want)
		}
	}
	writeFixtureFile(t, "water_bills_2up.pdf", waterPDF)

	garbageInvoices, err := models.Garbage.Invoices(ctx, fx.Tenant, fx.Property.ID, fx.Period, nil)
	if err != nil {
		t.Fatalf("Garbage.Invoices: %v", err)
	}
	if len(garbageInvoices) == 0 {
		t.Fatal("expected garbage invoices (only garbage-billed units)")
	}
	for _, inv := range garbageInvoices {
		lease := fx.Leases[unitIDForCode(fx, inv.HouseNo)]
		if lease != nil && !lease.GarbageBilled {
			t.Errorf("unit %s billed garbage but its lease has garbage_billed=false", inv.HouseNo)
		}
	}
	garbagePDF, err := pdf.BuildGarbageInvoices2Up(garbageInvoices)
	if err != nil {
		t.Fatalf("BuildGarbageInvoices2Up: %v", err)
	}
	garbageText := extractPDFText(t, garbagePDF)
	for _, want := range []string{"GARBAGE COLLECTION BILL", "Garbage Collection Fee", "TOTAL AMOUNT DUE TO DATE"} {
		if !strings.Contains(garbageText, want) {
			t.Errorf("garbage bill missing %q", want)
		}
	}
	writeFixtureFile(t, "garbage_bills_2up.pdf", garbagePDF)
}

func unitIDForCode(fx *fortyEightUnitFixture, code string) uuid.UUID {
	for _, u := range fx.Units {
		if u.UnitCode == code {
			return u.ID
		}
	}
	return uuid.Nil
}
