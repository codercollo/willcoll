package data

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/codercollo/willcoll/backend/internal/db/sqlc"
	"github.com/google/uuid"
)

// TestUnitQRIntegration covers the lifecycle on a real database: one live code
// per lease, rotation, scan counting, revocation when the lease ends, and
// isolation between firms.
func TestUnitQRIntegration(t *testing.T) {
	models, conn, _ := biModels(t)
	ctx := context.Background()

	tenant, landlord := seedTenant(t, conn)
	other, _ := seedTenant(t, conn)

	property := &Property{LandlordID: landlord, Name: "Runda Arcade", Location: "Runda", Slug: "qr-" + uuid.NewString()[:8]}
	if err := models.Properties.Insert(ctx, tenant, property); err != nil {
		t.Fatal(err)
	}
	unit := &Unit{PropertyID: property.ID, UnitCode: "A1", Status: "vacant"}
	if err := models.Units.Insert(ctx, tenant, unit); err != nil {
		t.Fatal(err)
	}
	lease := &Lease{UnitID: unit.ID, TenantName: "JOHN KAMAU", PrimaryPhone: "+254722000001", RentAmount: biMoney(t, "5000"),
		StartDate: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), Status: "active"}
	if err := models.Leases.Insert(ctx, tenant, tenant, lease); err != nil {
		t.Fatal(err)
	}

	// The units list flags leases that have a live sticker, for the unit cards.
	hasQR := func() bool {
		units, _, err := models.Units.GetAllForProperty(ctx, tenant, property.ID, UnitListFilter{}, Filters{Page: 1, PageSize: 10, Sort: "unit_code", SortSafelist: []string{"unit_code"}})
		if err != nil || len(units) != 1 || units[0].CurrentLease == nil {
			t.Fatalf("units list = %v, %v", units, err)
		}
		return units[0].CurrentLease.HasQR
	}
	if hasQR() {
		t.Error("a lease with no code is flagged as having one")
	}

	// --- GetOrCreate is idempotent: one live code per lease.
	first, err := models.UnitQR.GetOrCreate(ctx, tenant, tenant, lease.ID)
	if err != nil || first.Token == "" || first.RevokedAt != nil || first.ScanCount != 0 {
		t.Fatalf("GetOrCreate = %+v, %v", first, err)
	}
	wantScan, wantShort := FormatUnitQRCodes(first.Token, "A1")
	if !ValidQRCode(first.Token) || first.ScanCode != wantScan || first.ShortCode != wantShort {
		t.Errorf("token %q / scan code %q / short code %q, want scan %q / short %q",
			first.Token, first.ScanCode, first.ShortCode, wantScan, wantShort)
	}
	again, err := models.UnitQR.GetOrCreate(ctx, tenant, tenant, lease.ID)
	if err != nil || again.ID != first.ID || again.Token != first.Token {
		t.Fatalf("second GetOrCreate = %+v, %v; want the same code", again, err)
	}

	if !hasQR() {
		t.Error("a lease with a live code is not flagged in the units list")
	}

	// --- A scan resolves to the pay-page parts and nothing about the tenant.
	target, err := models.UnitQR.Resolve(ctx, first.ShortCode) // typed with dashes
	if err != nil || target.PropertySlug != property.Slug || target.UnitCode != "A1" || target.TenantID != tenant {
		t.Fatalf("Resolve = %+v, %v", target, err)
	}
	if _, err := models.UnitQR.Resolve(ctx, "k7qm 2xh9 ptrb"); !errors.Is(err, ErrQRInactive) {
		t.Errorf("an unknown code: err = %v, want ErrQRInactive", err)
	}
	if _, err := models.UnitQR.Resolve(ctx, "not-a-code"); !errors.Is(err, ErrQRInactive) {
		t.Errorf("a malformed code: err = %v, want ErrQRInactive", err)
	}
	// The bare token still resolves without its unit prefix (the URL form,
	// /q/A1-<token>, splits it off before this ever runs)...
	if _, err := models.UnitQR.Resolve(ctx, first.Token); err != nil {
		t.Errorf("the bare token: err = %v", err)
	}
	// ...but a sticker's code scanned with the WRONG unit's prefix (a mixup
	// — swapped stickers, or a tampered sticker) must not resolve, even
	// though the token itself is genuinely live.
	if _, err := models.UnitQR.Resolve(ctx, "B2-"+first.Token); !errors.Is(err, ErrQRInactive) {
		t.Errorf("wrong unit prefix: err = %v, want ErrQRInactive", err)
	}
	for i := 0; i < 3; i++ {
		if err := models.UnitQR.RecordScan(ctx, target); err != nil {
			t.Fatalf("RecordScan: %v", err)
		}
	}
	counted, _ := models.UnitQR.GetOrCreate(ctx, tenant, tenant, lease.ID)
	if counted.ScanCount != 3 || counted.LastScannedAt == nil {
		t.Errorf("after 3 scans: count %d, last scanned %v", counted.ScanCount, counted.LastScannedAt)
	}

	// --- Another firm cannot see, create or rotate a code on this lease.
	if _, err := models.UnitQR.GetOrCreate(ctx, other, other, lease.ID); !errors.Is(err, ErrLeaseNotFound) {
		t.Errorf("another firm's GetOrCreate: err = %v, want ErrLeaseNotFound", err)
	}
	if _, err := models.UnitQR.Rotate(ctx, other, other, lease.ID); !errors.Is(err, ErrLeaseNotFound) {
		t.Errorf("another firm's Rotate: err = %v, want ErrLeaseNotFound", err)
	}
	// RLS itself: even asking for the token by name inside the other firm's
	// transaction finds nothing.
	err = models.UnitQR.Store.ExecTenantTx(ctx, other, func(q sqlc.Querier) error {
		_, err := q.GetUnitQRCodeByToken(ctx, sqlc.GetUnitQRCodeByTokenParams{TenantID: other, Token: first.Token})
		if err == nil {
			t.Error("another firm read this firm's code row")
		}
		// Even naming the right firm, RLS scopes to the transaction's tenant.
		_, err = q.GetUnitQRCodeByToken(ctx, sqlc.GetUnitQRCodeByTokenParams{TenantID: tenant, Token: first.Token})
		if err == nil {
			t.Error("RLS did not hide the row from another firm's transaction")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// --- Rotate: the old code stops working, a new one works, and there is
	// exactly one live code.
	rotated, err := models.UnitQR.Rotate(ctx, tenant, tenant, lease.ID)
	if err != nil || rotated.Token == first.Token || rotated.ID == first.ID {
		t.Fatalf("Rotate = %+v, %v", rotated, err)
	}
	if _, err := models.UnitQR.Resolve(ctx, first.Token); !errors.Is(err, ErrQRInactive) {
		t.Errorf("the rotated-out sticker: err = %v, want ErrQRInactive", err)
	}
	if err := models.UnitQR.RecordScan(ctx, target); !errors.Is(err, ErrQRInactive) {
		t.Errorf("counting a scan of the revoked code: err = %v, want ErrQRInactive", err)
	}
	if _, err := models.UnitQR.Resolve(ctx, rotated.Token); err != nil {
		t.Errorf("the new sticker: %v", err)
	}
	var live int
	biTx(t, conn, tenant, func(tx *sql.Tx) {
		if err := tx.QueryRow(`SELECT count(*) FROM unit_qr_codes WHERE lease_id = $1 AND revoked_at IS NULL`, lease.ID).Scan(&live); err != nil {
			t.Fatal(err)
		}
	})
	if live != 1 {
		t.Errorf("%d live codes for the lease, want exactly 1", live)
	}

	// --- The database itself refuses a second live code (partial unique index).
	if _, err := conn.Exec(`INSERT INTO unit_qr_codes (token, lease_id, unit_id, property_id, tenant_id, created_by)
		VALUES ('ZZZZZZZZZZZZ', $1, $2, $3, $4, $4)`, lease.ID, unit.ID, property.ID, tenant); err == nil {
		t.Error("a second live code for one lease was accepted")
	}

	// --- Ending the lease revokes the code in the same transaction.
	ended := *lease
	ended.Status = "terminated"
	endDate := time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)
	ended.EndDate = &endDate
	if err := models.Leases.Update(ctx, &ended); err != nil {
		t.Fatalf("terminate lease: %v", err)
	}
	if _, err := models.UnitQR.Resolve(ctx, rotated.Token); !errors.Is(err, ErrQRInactive) {
		t.Errorf("a terminated lease's sticker: err = %v, want ErrQRInactive", err)
	}
	if _, err := models.UnitQR.GetOrCreate(ctx, tenant, tenant, lease.ID); !errors.Is(err, ErrQRLeaseNotActive) {
		t.Errorf("GetOrCreate on a terminated lease: err = %v, want ErrQRLeaseNotActive", err)
	}
	if _, err := models.UnitQR.Rotate(ctx, tenant, tenant, lease.ID); !errors.Is(err, ErrQRLeaseNotActive) {
		t.Errorf("Rotate on a terminated lease: err = %v, want ErrQRLeaseNotActive", err)
	}
}
