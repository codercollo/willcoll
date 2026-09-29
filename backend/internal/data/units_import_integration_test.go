package data

import (
	"context"
	"errors"
	"testing"

	"github.com/codercollo/willcoll/backend/internal/db"
	"github.com/google/uuid"
)

// TestImportUnitsIntegration: a bulk import is all-or-nothing, reports clashes
// by row, and never crosses managers.
func TestImportUnitsIntegration(t *testing.T) {
	conn := openTestDB(t)
	models := NewModelsFromStore(db.NewStore(conn), integrationTimeout)
	ctx := context.Background()

	tenant, landlord := seedTenant(t, conn)
	other, _ := seedTenant(t, conn)
	property := &Property{LandlordID: landlord, Name: "P", Location: "Nairobi", Slug: "p-" + uuid.NewString()[:8]}
	if err := models.Properties.Insert(ctx, tenant, property); err != nil {
		t.Fatal(err)
	}
	meter := "M-7"
	rows := []ImportUnit{{Row: 2, UnitCode: "A1", MeterNumber: &meter}, {Row: 3, UnitCode: "A2"}, {Row: 4, UnitCode: "A3"}}

	if units, err := models.Units.ImportUnits(ctx, tenant, property.ID, rows, true); err != nil || units != nil {
		t.Fatalf("dry run = %v, %v; want nothing written", units, err)
	}
	if n := countUnits(t, models, ctx, tenant, property.ID); n != 0 {
		t.Fatalf("a dry run wrote %d units", n)
	}

	units, err := models.Units.ImportUnits(ctx, tenant, property.ID, rows, false)
	if err != nil || len(units) != 3 || units[0].MeterNumber == nil || *units[0].MeterNumber != "M-7" || units[0].Status != UnitStatusVacant {
		t.Fatalf("import = %+v, %v", units, err)
	}

	// A second file that clashes on rows 3 and 5 imports nothing at all,
	// including its good rows.
	next := []ImportUnit{{Row: 2, UnitCode: "B1"}, {Row: 3, UnitCode: "A2"}, {Row: 4, UnitCode: "B2"}, {Row: 5, UnitCode: "A3"}}
	_, err = models.Units.ImportUnits(ctx, tenant, property.ID, next, false)
	var ie *ImportError
	if !errors.As(err, &ie) || !errors.Is(err, ErrImportRejected) || len(ie.Rows) != 2 || ie.Rows[0].Row != 3 || ie.Rows[1].Row != 5 {
		t.Fatalf("clashing import: err = %v", err)
	}
	if n := countUnits(t, models, ctx, tenant, property.ID); n != 3 {
		t.Errorf("units after a rejected import = %d, want 3: the good rows must not slip through", n)
	}

	if _, err := models.Units.ImportUnits(ctx, other, property.ID, rows, false); !errors.Is(err, ErrPropertyNotFound) {
		t.Errorf("another manager importing: err = %v, want ErrPropertyNotFound", err)
	}
}

func countUnits(t *testing.T, models Models, ctx context.Context, tenant, property uuid.UUID) int {
	t.Helper()
	_, meta, err := models.Units.GetAllForProperty(ctx, tenant, property, UnitListFilter{}, Filters{Page: 1, PageSize: 100, Sort: "unit_code"})
	if err != nil {
		t.Fatal(err)
	}
	return meta.TotalRecords
}
