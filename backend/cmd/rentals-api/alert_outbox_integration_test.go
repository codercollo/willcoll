package main

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	platformdata "github.com/codercollo/willcoll/backend/internal/platform/data"
	"github.com/codercollo/willcoll/backend/internal/rentals/data"
	"github.com/codercollo/willcoll/backend/internal/rentals/db/sqlc"
	"github.com/codercollo/willcoll/backend/internal/testenv"
	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// The owner-alert transactional outbox across the two databases (C4.0), against
// real Postgres: an alert commits with its action in willcoll_rentals, a relay
// hands it to willcoll_platform's email_outbox, and neither a rollback, a crash
// before the relay, nor a duplicate relay loses or doubles it.

type alertRig struct {
	rentals    data.Models
	platform   platformdata.Models
	rentalsApp *sql.DB
	rentalsAdm *sql.DB
	platAdm    *sql.DB
	firm       *platformdata.Manager
}

func newAlertRig(t *testing.T) *alertRig {
	t.Helper()
	dsns := testenv.Require(t, "WILLCOLL_TEST_DB_DSN", "WILLCOLL_TEST_ADMIN_DB_DSN",
		"WILLCOLL_TEST_PLATFORM_DB_DSN", "WILLCOLL_TEST_PLATFORM_ADMIN_DB_DSN")
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
	r := &alertRig{
		rentalsApp: open(dsns[0]), rentalsAdm: open(dsns[1]), platAdm: open(dsns[3]),
	}
	platApp := open(dsns[2])
	r.rentals = data.NewModels(r.rentalsApp, r.rentalsAdm, 5*time.Second)
	r.platform = platformdata.NewModels(platApp, r.platAdm, 5*time.Second)

	r.firm = &platformdata.Manager{FirmName: "Alert Rig Firm", Username: "u" + uuid.NewString()[:10],
		Email: uuid.NewString()[:8] + "@alertrig.example.com", Phone: "+254700000000", Status: platformdata.ManagerStatusPending}
	if err := r.firm.Password.Set("correct-horse-battery"); err != nil {
		t.Fatal(err)
	}
	if err := r.platform.Managers.Insert(context.Background(), r.firm); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = r.rentalsAdm.Exec(`DELETE FROM alert_outbox WHERE tenant_id = $1`, r.firm.ID) // admin has no DELETE: best effort
		_, _ = platApp.Exec(`DELETE FROM managers WHERE id = $1`, r.firm.ID)                 // cascades to email_outbox
	})
	return r
}

// alertHook is what the handlers' hooks do: capture the alert in the action's
// transaction.
func (r *alertRig) alertHook(key string) data.TxHook {
	return func(ctx context.Context, q sqlc.Querier) error {
		_, err := data.EnqueueAlertIn(ctx, q, r.firm.ID, key, data.AlertPayload{
			Recipient: r.firm.Email, FirmName: r.firm.FirmName, Actor: r.firm.FirmName + " (firm login)",
			IP: "198.51.100.7", Headline: "A ledger entry was reversed", Target: "KES 12,000.00", At: time.Now(),
		})
		return err
	}
}

func (r *alertRig) count(t *testing.T, conn *sql.DB, query string) (n int) {
	t.Helper()
	if err := conn.QueryRow(query, r.firm.ID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func (r *alertRig) rentalsAlerts(t *testing.T, status string) int {
	t.Helper()
	var n int
	if err := r.rentalsAdm.QueryRow(`SELECT count(*) FROM alert_outbox WHERE tenant_id = $1 AND status = $2`, r.firm.ID, status).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func (r *alertRig) platformEmails(t *testing.T) int {
	t.Helper()
	return r.count(t, r.platAdm, `SELECT count(*) FROM email_outbox WHERE manager_id = $1`)
}

// relay runs one relay pass with fresh wiring, as a restarted process would,
// and reports how many of THIS firm's alerts it settled. The scratch database is
// shared with other tests, whose alerts belong to firms the platform has never
// heard of; the relay leaves those pending (and counts them as failed), which
// is exactly what it should do, so only this firm's rows are asserted on.
func (r *alertRig) relay(t *testing.T) (relayed int) {
	t.Helper()
	before := r.rentalsAlerts(t, "relayed")
	fresh := data.NewModels(r.rentalsApp, r.rentalsAdm, 5*time.Second)
	if _, _, err := relayAlerts(context.Background(), fresh.Alerts, r.platform.Outbox, 1000); err != nil {
		t.Fatalf("relay: %v", err)
	}
	var lastErr sql.NullString
	_ = r.rentalsAdm.QueryRow(`SELECT last_error FROM alert_outbox WHERE tenant_id = $1 AND last_error IS NOT NULL LIMIT 1`, r.firm.ID).Scan(&lastErr)
	if lastErr.Valid {
		t.Fatalf("relaying this firm's alert failed: %s", lastErr.String)
	}
	return r.rentalsAlerts(t, "relayed") - before
}

// seedReversible makes a unit with one posted charge and returns the charge's
// ledger entry, ready to be reversed.
func (r *alertRig) seedReversible(t *testing.T) (entryID uuid.UUID) {
	t.Helper()
	ctx, tenant := context.Background(), r.firm.ID
	landlord := uuid.New()
	tenantSQL(t, r.rentalsApp, tenant, func(tx *sql.Tx) {
		if _, err := tx.Exec(`INSERT INTO landlords (id, tenant_id, name, phone) VALUES ($1, $2, 'L', '+254711111111')`, landlord, tenant); err != nil {
			t.Fatal(err)
		}
	})
	property := &data.Property{LandlordID: landlord, Name: "Alert Court", Location: "Nairobi", Slug: "ar-" + uuid.NewString()[:8]}
	if err := r.rentals.Properties.Insert(ctx, tenant, property); err != nil {
		t.Fatal(err)
	}
	unit := &data.Unit{PropertyID: property.ID, UnitCode: "A1", Status: "vacant"}
	if err := r.rentals.Units.Insert(ctx, tenant, unit); err != nil {
		t.Fatal(err)
	}
	lease := &data.Lease{UnitID: unit.ID, TenantName: "TENANT A1", PrimaryPhone: "+254722000001", RentAmount: money("5000"),
		StartDate: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), Status: "active"}
	if err := r.rentals.Leases.Insert(ctx, tenant, tenant, lease); err != nil {
		t.Fatal(err)
	}
	if err := r.rentals.Rent.PostManualCharge(ctx, tenant, tenant, unit.ID, data.LedgerTypeRent, money("5000"), "seed"); err != nil {
		t.Fatal(err)
	}
	tenantSQL(t, r.rentalsApp, tenant, func(tx *sql.Tx) {
		if err := tx.QueryRow(`SELECT id FROM ledger_entries WHERE tenant_id = $1 AND reference_type = 'charge' ORDER BY created_at LIMIT 1`, tenant).Scan(&entryID); err != nil {
			t.Fatal(err)
		}
	})
	return entryID
}

func TestAlertOutboxRollbackLeavesNoAlertOnEitherSide(t *testing.T) {
	r := newAlertRig(t)
	ctx := context.Background()
	entryID := r.seedReversible(t)
	boom := errors.New("a later step failed")

	// The reversal and its alert are written, then a later hook fails: the whole
	// transaction rolls back.
	_, err := r.rentals.Rent.ReverseEntryDetailed(ctx, r.firm.ID, r.firm.ID, entryID, "duplicate",
		func(ctx context.Context, q sqlc.Querier, _ data.Reversal) error {
			return r.alertHook("rev:"+entryID.String())(ctx, q)
		},
		func(context.Context, sqlc.Querier, data.Reversal) error { return boom })
	if !errors.Is(err, boom) {
		t.Fatalf("ReverseEntryDetailed err = %v, want the hook's error", err)
	}
	if n := r.rentalsAlerts(t, "pending") + r.rentalsAlerts(t, "relayed"); n != 0 {
		t.Fatalf("a rolled-back reversal left %d rentals alerts, want 0", n)
	}
	if n := r.relay(t); n != 0 {
		t.Fatalf("relay moved %d alerts after a rollback, want 0", n)
	}
	if n := r.platformEmails(t); n != 0 {
		t.Fatalf("a rolled-back reversal left %d platform emails, want 0", n)
	}

	// The entry was not reversed by the failed attempt, so the same reversal
	// works now, and this time the alert is there.
	if _, err := r.rentals.Rent.ReverseEntryDetailed(ctx, r.firm.ID, r.firm.ID, entryID, "duplicate",
		func(ctx context.Context, q sqlc.Querier, _ data.Reversal) error {
			return r.alertHook("rev:"+entryID.String())(ctx, q)
		}); err != nil {
		t.Fatalf("reversal after the rolled-back attempt: %v", err)
	}
	if n := r.rentalsAlerts(t, "pending"); n != 1 {
		t.Fatalf("a committed reversal queued %d rentals alerts, want 1", n)
	}
}

func TestAlertOutboxCrashBeforeRelayStillDelivers(t *testing.T) {
	r := newAlertRig(t)
	ctx := context.Background()
	entryID := r.seedReversible(t)

	if _, err := r.rentals.Rent.ReverseEntryDetailed(ctx, r.firm.ID, r.firm.ID, entryID, "duplicate",
		func(ctx context.Context, q sqlc.Querier, _ data.Reversal) error {
			return r.alertHook("rev:"+entryID.String())(ctx, q)
		}); err != nil {
		t.Fatal(err)
	}

	// The process dies here: the action and its alert are committed, nothing has
	// reached the platform.
	if n := r.rentalsAlerts(t, "pending"); n != 1 {
		t.Fatalf("pending rentals alerts = %d, want 1", n)
	}
	if n := r.platformEmails(t); n != 0 {
		t.Fatalf("platform emails before the relay = %d, want 0", n)
	}

	// A restarted process's relay delivers it, once.
	if n := r.relay(t); n != 1 {
		t.Fatalf("relay moved %d alerts, want 1", n)
	}
	if n := r.platformEmails(t); n != 1 {
		t.Fatalf("platform emails after the relay = %d, want 1", n)
	}
	if n := r.rentalsAlerts(t, "relayed"); n != 1 {
		t.Errorf("relayed rentals alerts = %d, want 1", n)
	}
	if n := r.rentalsAlerts(t, "pending"); n != 0 {
		t.Errorf("pending rentals alerts after the relay = %d, want 0", n)
	}
}

func TestAlertOutboxDuplicateRelaySendsNoDuplicateEmail(t *testing.T) {
	r := newAlertRig(t)
	ctx := context.Background()

	// Written directly in a tenant transaction, as any action's hook does it.
	if err := r.rentalsTx(ctx, r.alertHook("dup:1")); err != nil {
		t.Fatal(err)
	}
	// The same event enqueued again (a retried handler) is still one row.
	if err := r.rentalsTx(ctx, r.alertHook("dup:1")); err != nil {
		t.Fatal(err)
	}
	if n := r.rentalsAlerts(t, "pending"); n != 1 {
		t.Fatalf("one event enqueued twice left %d rentals alerts, want 1", n)
	}

	if n := r.relay(t); n != 1 {
		t.Fatalf("first relay moved %d, want 1", n)
	}

	// The relay dies after the platform enqueue and before marking: the row is
	// pending again, and relaying it again queues nothing new.
	if _, err := r.rentalsAdm.Exec(`UPDATE alert_outbox SET status = 'pending', relayed_at = NULL WHERE tenant_id = $1`, r.firm.ID); err != nil {
		t.Fatal(err)
	}
	if n := r.relay(t); n != 1 {
		t.Fatalf("second relay settled %d, want 1", n)
	}
	if n := r.relay(t); n != 0 {
		t.Fatalf("third relay moved %d, want 0 (nothing pending)", n)
	}
	if n := r.platformEmails(t); n != 1 {
		t.Fatalf("platform emails after three relays = %d, want 1", n)
	}
}

// rentalsTx runs hooks in a tenant transaction of their own.
func (r *alertRig) rentalsTx(ctx context.Context, hooks ...data.TxHook) error {
	return r.rentals.Properties.Store.ExecTenantTx(ctx, r.firm.ID, func(q sqlc.Querier) error {
		for _, h := range hooks {
			if err := h(ctx, q); err != nil {
				return err
			}
		}
		return nil
	})
}
