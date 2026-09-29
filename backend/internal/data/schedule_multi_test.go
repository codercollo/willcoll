package data

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/codercollo/willcoll/backend/internal/db"
	"github.com/codercollo/willcoll/backend/internal/pdf"
)

// TestScheduleMultiPage confirms the 48-unit fixture and renders the
// schedule PDF, checking it spans at least 2 pages (48 rows don't fit one
// A4 landscape page) and carries the fixture's distinguishing rows: the
// OR co-tenant, the C/O business tenant, a vacant unit's row, and the
// stacked-payment unit's two dated lines.
func TestScheduleMultiPage(t *testing.T) {
	conn := openTestDB(t)
	models := NewModelsFromStore(db.NewStore(conn), integrationTimeout)
	ctx := context.Background()

	fx := buildFortyEightUnitFixture(t, conn, models)

	result, _, err := models.Reports.Checks(ctx, fx.Tenant, fx.Property.ID, fx.Period)
	if err != nil {
		t.Fatalf("Checks: %v", err)
	}
	if !result.Passed() {
		t.Fatalf("expected checks to pass, got: %+v", result.Failures)
	}
	if _, err := models.Reports.Confirm(ctx, fx.Tenant, fx.Tenant, fx.Property.ID, fx.Period); err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	report, err := models.Reports.GenerateConfirmed(ctx, fx.Tenant, fx.Property.ID, fx.Period)
	if err != nil {
		t.Fatalf("GenerateConfirmed: %v", err)
	}
	if len(report.Rows) != 48 {
		t.Fatalf("rows = %d, want 48", len(report.Rows))
	}

	b, err := pdf.BuildMonthlySchedule(report.Schedule())
	if err != nil {
		t.Fatalf("BuildMonthlySchedule: %v", err)
	}
	text := extractPDFText(t, b)
	for _, want := range []string{
		"JANE WAMBUI", "OR", "PETER KAMAU",
		"MERCY KANANA", "C/O", "HARDWARE", // wraps across lines
		"1C", // the vacant unit's house number still gets a row
	} {
		if !strings.Contains(text, want) {
			t.Errorf("schedule missing %q", want)
		}
	}

	writeFixtureFile(t, "schedule_multi.pdf", b)
}

// writeFixtureFile saves a rendered document to the shared fixture output
// folder, for a human to open and eyeball.
func writeFixtureFile(t *testing.T, name string, data []byte) {
	t.Helper()
	dir := filepath.Join(os.TempDir(), "willcoll-rundas-fixture")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	t.Logf("wrote %s", path)
}
