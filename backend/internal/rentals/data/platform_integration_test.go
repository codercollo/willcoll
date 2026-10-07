package data

import (
	"context"
	"database/sql"
	"github.com/codercollo/willcoll/backend/internal/testenv"
	"testing"

	"github.com/codercollo/willcoll/backend/internal/rentals/db"
	"github.com/google/uuid"
)

// TestPlatformIntegration checks rentals-api's Super Admin pool against real
// Postgres: the health reads and the cross-tenant tenant listing run on
// willcoll_admin (BYPASSRLS), where willcoll_app's RLS would hide them.
// Manager/subscription oversight and suspend/reinstate moved to
// platform-api (build-phases.txt 3i-b). Needs WILLCOLL_TEST_ADMIN_DB_DSN as
// well.
func TestPlatformIntegration(t *testing.T) {
	appConn := openTestDB(t)
	adminDSN := testenv.Require(t, "WILLCOLL_TEST_ADMIN_DB_DSN")[0]
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

	// The tenant listing (the usage recount sweep) sees across tenants on
	// the admin pool...
	ids, err := models.Platform.ListTenantIDs(ctx)
	if err != nil {
		t.Fatalf("Platform.ListTenantIDs: %v", err)
	}
	found := false
	for _, id := range ids {
		found = found || id == tenant
	}
	if !found {
		t.Error("ListTenantIDs missed the seeded tenant")
	}

	// ...while the identical query on the app pool can't: with no tenant
	// scope set, RLS hides or refuses it. That's why it needs its own role.
	appOnly := NewModelsFromStore(db.NewStore(appConn), 0)
	if blind, err := appOnly.Platform.ListTenantIDs(ctx); err == nil && len(blind) != 0 {
		t.Errorf("app-pool listing saw %d tenants; RLS should hide them", len(blind))
	} else if err != nil {
		t.Logf("app pool refused cross-tenant listing, as it should: %v", err)
	}

	if h, _, err := models.Platform.DatabaseHealth(ctx); err != nil || h.DatabaseSizeBytes <= 0 || h.Connections < 1 || len(h.LargestTables) == 0 {
		t.Errorf("DatabaseHealth = %+v, %v", h, err)
	}

	// system_metadata is readable as willcoll_admin (000022).
	if _, err := models.Platform.LastBackup(ctx); err != nil {
		t.Errorf("LastBackup: %v", err)
	}
}
