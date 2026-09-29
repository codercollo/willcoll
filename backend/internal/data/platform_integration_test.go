package data

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"testing"

	"github.com/codercollo/willcoll/backend/internal/db"
	"github.com/google/uuid"
)

// TestPlatformIntegration checks the Super Admin's two connection pools
// against real Postgres: oversight on willcoll_admin sees across tenants,
// where willcoll_app's RLS would hide them; admin provisioning on
// willcoll_app. Needs WILLCOLL_TEST_ADMIN_DB_DSN as well.
func TestPlatformIntegration(t *testing.T) {
	appConn := openTestDB(t)
	adminDSN := os.Getenv("WILLCOLL_TEST_ADMIN_DB_DSN")
	if adminDSN == "" {
		t.Skip("WILLCOLL_TEST_ADMIN_DB_DSN not set; skipping")
	}
	adminConn, err := sql.Open("pgx", adminDSN)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { adminConn.Close() })

	ctx := context.Background()
	models := NewModels(appConn, adminConn, integrationTimeout)

	tenant, landlord := seedTenant(t, appConn)
	property := &Property{LandlordID: landlord, Name: "P", Location: "Nairobi", Slug: "p-" + uuid.NewString()[:8]}
	if err := models.Properties.Insert(ctx, tenant, property); err != nil {
		t.Fatal(err)
	}
	if err := models.Units.Insert(ctx, tenant, &Unit{PropertyID: property.ID, UnitCode: "1A", Status: UnitStatusVacant}); err != nil {
		t.Fatal(err)
	}

	// Oversight runs as willcoll_admin (BYPASSRLS) and counts the tenant's rows.
	overview, err := models.Platform.GetManager(ctx, tenant)
	if err != nil {
		t.Fatalf("Platform.GetManager: %v", err)
	}
	if overview.PropertyCount != 1 || overview.UnitCount != 1 || overview.Subscription != nil {
		t.Errorf("overview = %+v; want 1 property, 1 unit, no subscription", overview)
	}

	// The identical query on the app pool can't see across tenants: with
	// no tenant scope set, RLS fails it closed. That's why /v1/admin/* needs
	// its own role.
	appOnly := NewModelsFromStore(db.NewStore(appConn), 0)
	if blind, err := appOnly.Platform.GetManager(ctx, tenant); err == nil && blind.PropertyCount != 0 {
		t.Errorf("app-pool oversight saw %d properties; RLS should hide them", blind.PropertyCount)
	} else if err != nil {
		t.Logf("app pool refused cross-tenant oversight, as it should: %v", err)
	}

	list, meta, err := models.Platform.ListManagers(ctx, ManagerStatusPending, Filters{Page: 1, PageSize: 100, Sort: "-created_at"})
	if err != nil || meta.TotalRecords < 1 {
		t.Fatalf("Platform.ListManagers: %v, %+v", err, meta)
	}
	found := false
	for _, m := range list {
		found = found || m.ID == tenant
	}
	if !found {
		t.Error("ListManagers(pending) missed the seeded firm")
	}

	// Suspend: once, again (no-op), and an unknown id.
	if err := models.Platform.SuspendManager(ctx, tenant); err != nil {
		t.Fatalf("SuspendManager: %v", err)
	}
	if err := models.Platform.SuspendManager(ctx, tenant); err != nil {
		t.Errorf("second SuspendManager: %v", err)
	}
	if err := models.Platform.SuspendManager(ctx, uuid.New()); !errors.Is(err, ErrRecordNotFound) {
		t.Errorf("SuspendManager(unknown) = %v, want ErrRecordNotFound", err)
	}
	if m, _ := models.Managers.Get(ctx, tenant); m == nil || m.Status != ManagerStatusSuspended {
		t.Errorf("manager status after suspend = %+v", m)
	}

	// Reinstate: the seeded firm never activated, so it returns to pending.
	if err := models.Platform.ReinstateManager(ctx, tenant); err != nil {
		t.Fatalf("ReinstateManager: %v", err)
	}
	if m, _ := models.Managers.Get(ctx, tenant); m == nil || m.Status != ManagerStatusPending {
		t.Errorf("status after reinstating a never-activated firm = %+v, want pending", m)
	}
	if err := models.Platform.ReinstateManager(ctx, tenant); err != nil {
		t.Errorf("reinstating a firm that isn't suspended should be a no-op: %v", err)
	}
	if err := models.Platform.ReinstateManager(ctx, uuid.New()); !errors.Is(err, ErrRecordNotFound) {
		t.Errorf("ReinstateManager(unknown) = %v, want ErrRecordNotFound", err)
	}

	if _, _, err := models.Platform.ListSubscriptions(ctx, "", Filters{Page: 1, PageSize: 20, Sort: "current_period_end"}); err != nil {
		t.Errorf("ListSubscriptions: %v", err)
	}
	if h, _, err := models.Platform.DatabaseHealth(ctx); err != nil || h.DatabaseSizeBytes <= 0 || h.Connections < 1 || len(h.LargestTables) == 0 {
		t.Errorf("DatabaseHealth = %+v, %v", h, err)
	}

	// system_metadata is readable as willcoll_admin (000022).
	if _, err := models.Platform.LastBackup(ctx); err != nil {
		t.Errorf("LastBackup: %v", err)
	}

	// Admin provisioning. The admins table allows exactly one row (000023),
	// so this only runs against an empty table: it must never overwrite a
	// real Super Admin in a development database.
	var admins int
	if err := appConn.QueryRow(`SELECT count(*) FROM admins`).Scan(&admins); err != nil {
		t.Fatal(err)
	}
	if admins > 0 {
		t.Log("admins table not empty; skipping provisioning checks to leave the real admin alone")
		return
	}

	email := "admin-" + uuid.NewString()[:8] + "@willcoll.test"
	t.Cleanup(func() { appConn.Exec(`DELETE FROM admins`) })

	first, err := models.Admins.Provision(ctx, "Root", email, "first-password-123")
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	again, err := models.Admins.Provision(ctx, "Root", email, "first-password-123")
	if err != nil || again.ID != first.ID {
		t.Fatalf("re-Provision: %v, %v", again, err)
	}
	if _, err := models.Admins.Provision(ctx, "Root", email, "rotated-password-456"); err != nil {
		t.Fatalf("rotate: %v", err)
	}
	got, err := models.Admins.GetByEmail(ctx, email)
	if err != nil {
		t.Fatal(err)
	}
	if ok, _ := got.Password.Matches("rotated-password-456"); !ok {
		t.Error("password was not rotated")
	}
	if ok, _ := got.Password.Matches("first-password-123"); ok {
		t.Error("old password still works after rotation")
	}

	// A new ADMIN_EMAIL re-points the one row rather than adding a second.
	newEmail := "admin-" + uuid.NewString()[:8] + "@willcoll.test"
	moved, err := models.Admins.Provision(ctx, "Root", newEmail, "rotated-password-456")
	if err != nil {
		t.Fatalf("Provision(new email): %v", err)
	}
	if moved.ID != first.ID {
		t.Errorf("changing the email created a new admin (%s) instead of updating %s", moved.ID, first.ID)
	}
	if err := appConn.QueryRow(`SELECT count(*) FROM admins`).Scan(&admins); err != nil || admins != 1 {
		t.Errorf("admins rows = %d (err %v), want exactly 1", admins, err)
	}
	if _, err := appConn.Exec(`INSERT INTO admins (name, email, password_hash) VALUES ('x', 'x@x.co', '\x00')`); err == nil {
		t.Error("a second admin row was accepted; the singleton index should refuse it")
	}
}
