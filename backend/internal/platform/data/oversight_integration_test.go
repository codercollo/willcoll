package data

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/codercollo/willcoll/backend/internal/platform/db"
	"github.com/codercollo/willcoll/backend/internal/testenv"
	"github.com/codercollo/willcoll/backend/pkg/moneyfmt"
	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// TestOversightIntegration runs the Super Admin's oversight SQL (the
// queries in db/query/oversight.sql, which no mock can check) against a real
// willcoll_platform: reads across tenants on the BYPASSRLS admin pool, the
// RLS wall the app pool keeps, and the suspend/reinstate status moves.
// Needs WILLCOLL_TEST_PLATFORM_DB_DSN and WILLCOLL_TEST_PLATFORM_ADMIN_DB_DSN
// (scripts/test_integration.sh provides both).
func TestOversightIntegration(t *testing.T) {
	dsns := testenv.Require(t, "WILLCOLL_TEST_PLATFORM_DB_DSN", "WILLCOLL_TEST_PLATFORM_ADMIN_DB_DSN")
	open := func(dsn string) *sql.DB {
		c, err := sql.Open("pgx", dsn)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = c.Close() })
		if err := c.Ping(); err != nil {
			t.Fatal(err)
		}
		return c
	}
	appConn, adminConn := open(dsns[0]), open(dsns[1])
	models := NewModels(appConn, adminConn, 5*time.Second)
	ctx := context.Background()

	tx := func(tenant uuid.UUID, fn func(tx *sql.Tx)) {
		t.Helper()
		tx, err := appConn.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback() }()
		if _, err := tx.Exec(`SELECT set_config('app.tenant_id', $1, true)`, tenant.String()); err != nil {
			t.Fatal(err)
		}
		fn(tx)
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}

	newFirm := func(name string, activated bool) *Manager {
		m := &Manager{FirmName: name, Username: "u" + uuid.NewString()[:10], Email: uuid.NewString()[:8] + "@oversight.example.com", Phone: "+254700000000", Status: ManagerStatusPending}
		if err := m.Password.Set("correct-horse-battery"); err != nil {
			t.Fatal(err)
		}
		if err := models.Managers.Insert(ctx, m); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _, _ = appConn.Exec(`DELETE FROM managers WHERE id = $1`, m.ID) })
		if activated {
			now := time.Now()
			m.Status, m.ActivatedAt = ManagerStatusActive, &now
			if err := models.Managers.Update(ctx, m); err != nil {
				t.Fatal(err)
			}
		}
		return m
	}
	firm := newFirm("Oversight Firm", true)
	pending := newFirm("Never Activated Firm", false)

	// Two staff (one invited), a Rentals module on a trial, reported usage and
	// a paid subscription: every column the overview reads.
	for _, st := range []string{"active", "invited"} {
		tx(firm.ID, func(tx *sql.Tx) {
			if _, err := tx.Exec(`INSERT INTO staff (manager_id, name, email, password_hash, role, module_access, status)
				VALUES ($1, 'S', $2, '\x00', 'officer', '{rentals}', $3)`, firm.ID, uuid.NewString()[:8]+"@s.example.com", st); err != nil {
				t.Fatal(err)
			}
		})
	}
	if _, err := models.Modules.StartTrial(ctx, firm.ID, ProductRentals, 7); err != nil {
		t.Fatal(err)
	}
	if _, err := models.Usage.Upsert(ctx, firm.ID, ProductRentals, 12); err != nil {
		t.Fatal(err)
	}
	price, err := moneyfmt.Parse("3500")
	if err != nil {
		t.Fatal(err)
	}
	plan, err := models.Plans.Create(ctx, PlanInput{Product: ProductRentals, Name: "Oversight " + uuid.NewString()[:6], Price: price,
		BillingInterval: "monthly", IncludedSeats: 1, Features: []string{}, PricingType: PricingFlat}, false)
	if err != nil {
		t.Fatal(err)
	}
	tx(firm.ID, func(tx *sql.Tx) {
		if _, err := tx.Exec(`INSERT INTO subscriptions (manager_id, product, plan_id, status, current_period_start, current_period_end)
			VALUES ($1, 'rentals', $2, 'active', current_date, current_date + 30)`, firm.ID, plan.ID); err != nil {
			t.Fatal(err)
		}
	})

	// --- Reads on the admin pool.
	o, err := models.Oversight.GetManager(ctx, firm.ID)
	if err != nil {
		t.Fatalf("GetManager: %v", err)
	}
	if o.StaffCount != 2 || o.UnitCount != 12 || o.Subscription == nil || o.Subscription.Status != SubscriptionActive {
		t.Errorf("overview = staff %d, units %d, subscription %+v; want 2, 12, active", o.StaffCount, o.UnitCount, o.Subscription)
	}
	if len(o.Modules) != 1 || o.Modules[0].Product != ProductRentals || o.Modules[0].Status != ModuleStatusTrialing || o.Modules[0].TrialEndsAt == nil {
		t.Errorf("modules = %+v", o.Modules)
	}
	if len(o.Subscriptions) != 1 || o.Subscriptions[0].PlanName != plan.Name || o.Subscriptions[0].Product != ProductRentals {
		t.Errorf("subscriptions = %+v", o.Subscriptions)
	}
	if len(o.Usage) != 1 || o.Usage[0].BillableCount != 12 {
		t.Errorf("usage = %+v", o.Usage)
	}
	if _, err := models.Oversight.GetManager(ctx, uuid.New()); !errors.Is(err, ErrRecordNotFound) {
		t.Errorf("GetManager(unknown) = %v, want ErrRecordNotFound", err)
	}
	if ids, err := models.Oversight.ListStaffIDs(ctx, firm.ID); err != nil || len(ids) != 2 {
		t.Errorf("ListStaffIDs = %v, %v; want both staff, whatever their status", ids, err)
	}

	list, meta, err := models.Oversight.ListManagers(ctx, ManagerStatusActive, Filters{Page: 1, PageSize: 100, Sort: "-created_at"})
	if err != nil || meta.TotalRecords < 1 {
		t.Fatalf("ListManagers: %v %+v", err, meta)
	}
	var found *ManagerOverview
	for _, m := range list {
		if m.ID == firm.ID {
			found = m
		}
		if m.ID == pending.ID {
			t.Error("ListManagers(active) returned a pending firm")
		}
	}
	if found == nil || len(found.Modules) != 1 || len(found.Usage) != 1 {
		t.Errorf("ListManagers(active) missed the firm or its detail: %+v", found)
	}
	if empty, err := models.Oversight.GetManager(ctx, pending.ID); err != nil || empty.Subscription != nil || len(empty.Modules) != 0 || empty.UnitCount != 0 {
		t.Errorf("a firm with nothing yet: %+v, %v", empty, err)
	}

	subs, _, err := models.Oversight.ListSubscriptions(ctx, ProductRentals, SubscriptionActive, Filters{Page: 1, PageSize: 100, Sort: "current_period_end"})
	if err != nil {
		t.Fatal(err)
	}
	ours := 0
	for _, s := range subs {
		if s.ManagerID == firm.ID {
			ours++
			if s.PlanName != plan.Name || s.Product != ProductRentals {
				t.Errorf("subscription row = %+v", s)
			}
		}
	}
	if ours != 1 {
		t.Errorf("ListSubscriptions(rentals, active) returned %d rows for the firm, want 1", ours)
	}
	if other, _, err := models.Oversight.ListSubscriptions(ctx, ProductEstates, "", Filters{Page: 1, PageSize: 100, Sort: "current_period_end"}); err != nil {
		t.Fatal(err)
	} else {
		for _, s := range other {
			if s.ManagerID == firm.ID {
				t.Error("the estates filter returned a rentals subscription")
			}
		}
	}

	// --- RLS: the same overview on the app pool, with no tenant scope, must
	// not see across tenants (it errors, or counts nothing).
	appOnly := OversightModel{Store: db.NewStore(appConn)}
	if blind, err := appOnly.GetManager(ctx, firm.ID); err == nil && (blind.StaffCount != 0 || blind.UnitCount != 0) {
		t.Errorf("the app pool saw across tenants: staff %d, units %d", blind.StaffCount, blind.UnitCount)
	}

	// --- Suspend / reinstate.
	if err := models.Oversight.SuspendManager(ctx, firm.ID); err != nil {
		t.Fatalf("SuspendManager: %v", err)
	}
	if err := models.Oversight.SuspendManager(ctx, firm.ID); err != nil {
		t.Errorf("second SuspendManager should be a no-op: %v", err)
	}
	if err := models.Oversight.SuspendManager(ctx, uuid.New()); !errors.Is(err, ErrRecordNotFound) {
		t.Errorf("SuspendManager(unknown) = %v", err)
	}
	if m, err := models.Managers.Get(ctx, firm.ID); err != nil || m.Status != ManagerStatusSuspended {
		t.Errorf("status after suspend = %+v, %v", m, err)
	}
	if err := models.Oversight.ReinstateManager(ctx, firm.ID); err != nil {
		t.Fatalf("ReinstateManager: %v", err)
	}
	if m, _ := models.Managers.Get(ctx, firm.ID); m == nil || m.Status != ManagerStatusActive {
		t.Errorf("an activated firm reinstates to active, got %+v", m)
	}
	if err := models.Oversight.SuspendManager(ctx, pending.ID); err != nil {
		t.Fatal(err)
	}
	if err := models.Oversight.ReinstateManager(ctx, pending.ID); err != nil {
		t.Fatal(err)
	}
	if m, _ := models.Managers.Get(ctx, pending.ID); m == nil || m.Status != ManagerStatusPending {
		t.Errorf("a never-activated firm reinstates to pending, got %+v", m)
	}
	if err := models.Oversight.ReinstateManager(ctx, firm.ID); err != nil {
		t.Errorf("reinstating a firm that isn't suspended should be a no-op: %v", err)
	}
}
