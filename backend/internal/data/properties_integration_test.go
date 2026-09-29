package data

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"testing"

	"github.com/codercollo/willcoll/backend/internal/db"
	"github.com/codercollo/willcoll/backend/pkg/moneyfmt"
	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// openTestDB connects as the API role (willcoll_app, subject to RLS) using
// WILLCOLL_TEST_DB_DSN, skipping the test when it isn't set so `go test
// ./...` never needs Postgres.
func openTestDB(t *testing.T) *sql.DB {
	t.Helper()

	dsn := os.Getenv("WILLCOLL_TEST_DB_DSN")
	if dsn == "" {
		t.Skip("WILLCOLL_TEST_DB_DSN not set; skipping Postgres integration test")
	}

	conn, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })

	if err := conn.Ping(); err != nil {
		t.Fatal(err)
	}
	return conn
}

// seedTenant creates a manager and one landlord for it, removing both (and,
// by cascade, their properties) when the test ends.
func seedTenant(t *testing.T, conn *sql.DB) (tenantID, landlordID uuid.UUID) {
	t.Helper()
	ctx := context.Background()

	tenantID = uuid.New()
	_, err := conn.ExecContext(ctx, `
		INSERT INTO managers (id, firm_name, username, email, phone, password_hash)
		VALUES ($1, 'Test Firm', $2, $3, '+254700000000', '\x00')`,
		tenantID, "u-"+tenantID.String(), tenantID.String()+"@example.com")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Exec(`DELETE FROM managers WHERE id = $1`, tenantID) })

	landlordID = uuid.New()

	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `SELECT set_config('app.tenant_id', $1, true)`, tenantID.String()); err != nil {
		t.Fatal(err)
	}
	// Bank details so any water/garbage bill's payment particulars aren't
	// blocked as missing by default; a test that wants to exercise that
	// block clears them itself.
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO landlords (id, tenant_id, name, phone, bank_name, bank_account_name, bank_account_number)
		VALUES ($1, $2, 'Landlord', '+254711111111', 'KCB', 'Landlord', '0123456789')`,
		landlordID, tenantID); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	return tenantID, landlordID
}

func TestPropertyModelIntegration(t *testing.T) {
	conn := openTestDB(t)
	store := db.NewStore(conn)
	m := PropertyModel{Store: store}
	ctx := context.Background()

	tenantA, landlordA := seedTenant(t, conn)
	tenantB, landlordB := seedTenant(t, conn)

	newProperty := func(name string, landlord uuid.UUID) *Property {
		return &Property{
			LandlordID:           landlord,
			Name:                 name,
			Location:             "Nairobi",
			Slug:                 "p-" + uuid.NewString()[:8],
			GarbageFee:           moneyfmt.FromCents(30000),
			WaterRatePerUnit:     moneyfmt.FromCents(15050),
			ManagementFeePercent: 5,
		}
	}

	// Insert + money round-trip.
	kiwi := newProperty("Kiwi Place", landlordA)
	if err := m.Insert(ctx, tenantA, kiwi); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if kiwi.ID == uuid.Nil || kiwi.Version != 1 || kiwi.TenantID != tenantA {
		t.Fatalf("Insert did not populate row: %+v", kiwi)
	}
	if got := kiwi.WaterRatePerUnit.String(); got != "150.50" {
		t.Errorf("water rate round-trip = %s, want 150.50", got)
	}

	arcade := newProperty("Runda Arcade", landlordA)
	if err := m.Insert(ctx, tenantA, arcade); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	// Cross-tenant landlord is rejected even though the FK would accept it.
	if err := m.Insert(ctx, tenantA, newProperty("Stolen", landlordB)); !errors.Is(err, ErrLandlordNotFound) {
		t.Errorf("Insert with another tenant's landlord: err = %v, want ErrLandlordNotFound", err)
	}

	// Slugs are globally unique.
	dup := newProperty("Dup", landlordB)
	dup.Slug = kiwi.Slug
	if err := m.Insert(ctx, tenantB, dup); !errors.Is(err, ErrDuplicateSlug) {
		t.Errorf("Insert with duplicate slug: err = %v, want ErrDuplicateSlug", err)
	}

	// Tenant isolation: B can't see A's property.
	if _, err := m.Get(ctx, tenantB, kiwi.ID); !errors.Is(err, ErrRecordNotFound) {
		t.Errorf("Get across tenants: err = %v, want ErrRecordNotFound", err)
	}

	// Sorting and paging.
	filters := Filters{Page: 1, PageSize: 1, Sort: "-name", SortSafelist: []string{"name", "-name"}}
	page, meta, err := m.GetAll(ctx, tenantA, nil, filters)
	if err != nil {
		t.Fatalf("GetAll: %v", err)
	}
	if len(page) != 1 || page[0].Name != "Runda Arcade" || meta.TotalRecords != 2 || meta.LastPage != 2 {
		t.Errorf("GetAll -name page 1 = %v, meta %+v", page, meta)
	}

	none, meta, err := m.GetAll(ctx, tenantB, nil, Filters{Page: 1, PageSize: 20, Sort: "name"})
	if err != nil || len(none) != 0 || meta.TotalRecords != 0 {
		t.Errorf("GetAll for tenant B = %v, %+v, %v; want empty", none, meta, err)
	}

	// Update bumps the version; a stale version is an edit conflict.
	got, err := m.Get(ctx, tenantA, kiwi.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	stale := *got
	got.Name = "Kiwi Place II"
	if err := m.Update(ctx, got); err != nil || got.Version != 2 {
		t.Fatalf("Update: err = %v, version = %d", err, got.Version)
	}
	stale.Name = "Lost update"
	if err := m.Update(ctx, &stale); !errors.Is(err, ErrEditConflict) {
		t.Errorf("stale Update: err = %v, want ErrEditConflict", err)
	}

	// Soft delete: the row is archived, not removed, and vanishes from the API.
	if err := m.Delete(ctx, tenantB, kiwi.ID); !errors.Is(err, ErrRecordNotFound) {
		t.Errorf("Delete across tenants: err = %v, want ErrRecordNotFound", err)
	}
	if err := m.Delete(ctx, tenantA, kiwi.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := m.Get(ctx, tenantA, kiwi.ID); !errors.Is(err, ErrRecordNotFound) {
		t.Errorf("Get after Delete: err = %v, want ErrRecordNotFound", err)
	}
	if err := m.Delete(ctx, tenantA, kiwi.ID); !errors.Is(err, ErrRecordNotFound) {
		t.Errorf("second Delete: err = %v, want ErrRecordNotFound", err)
	}
	if err := m.Update(ctx, got); !errors.Is(err, ErrEditConflict) {
		t.Errorf("Update after Delete: err = %v, want ErrEditConflict", err)
	}
	remaining, meta, err := m.GetAll(ctx, tenantA, nil, Filters{Page: 1, PageSize: 20, Sort: "name"})
	if err != nil || len(remaining) != 1 || remaining[0].ID != arcade.ID || meta.TotalRecords != 1 {
		t.Errorf("GetAll after Delete = %v, %+v, %v; want only Runda Arcade", remaining, meta, err)
	}

	// Read the raw row as the API role to confirm it was archived, not removed.
	var archivedAt sql.NullTime
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `SELECT set_config('app.tenant_id', $1, true)`, tenantA.String()); err != nil {
		t.Fatal(err)
	}
	if err := tx.QueryRowContext(ctx, `SELECT deleted_at FROM properties WHERE id = $1`, kiwi.ID).Scan(&archivedAt); err != nil {
		t.Fatalf("archived row should still exist: %v", err)
	}
	if !archivedAt.Valid {
		t.Error("deleted_at not set on archived row")
	}

	// The API role cannot hard-delete a property at all.
	if _, err := tx.ExecContext(ctx, `DELETE FROM properties WHERE id = $1`, arcade.ID); err == nil {
		t.Error("hard DELETE on properties succeeded; want permission denied")
	}
}
