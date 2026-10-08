package main

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/codercollo/willcoll/backend/internal/rentals/data"
	"github.com/codercollo/willcoll/backend/internal/testenv"
	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// A payment intent's status is readable only by a pay session of the same
// firm AND the same unit (authorization.md section 5: the intent id in the
// URL is a bare uuid, so tenancy has to come from the signed session and from
// RLS, never from the id). The same-firm other-unit case is unit-tested in
// pay_test.go; this is the cross-firm case against a real database, where
// row-level security is what answers.
func TestPaymentIntentStatusIsTenantIsolated(t *testing.T) {
	dsns := testenv.Require(t, "WILLCOLL_TEST_DB_DSN", "WILLCOLL_TEST_ADMIN_DB_DSN")
	open := func(dsn string) *sql.DB {
		db, err := sql.Open("pgx", dsn)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { db.Close() })
		if err := db.Ping(); err != nil {
			t.Fatal(err)
		}
		return db
	}
	conn, adminConn := open(dsns[0]), open(dsns[1])
	models := data.NewModels(conn, adminConn, 5*time.Second)
	ctx := context.Background()

	type firm struct {
		tenant   uuid.UUID
		phone    string
		target   data.PayTarget
		intentID uuid.UUID
	}
	newFirm := func(unitCode, phone string) firm {
		tenant, landlord := uuid.New(), uuid.New()
		tenantSQL(t, conn, tenant, func(tx *sql.Tx) {
			if _, err := tx.Exec(`INSERT INTO landlords (id, tenant_id, name, phone) VALUES ($1, $2, 'L', '+254711111111')`, landlord, tenant); err != nil {
				t.Fatal(err)
			}
		})
		channel := "CH-" + uuid.NewString()[:8]
		property := &data.Property{LandlordID: landlord, Name: "Court " + unitCode, Location: "Nairobi",
			Slug: "xt-" + uuid.NewString()[:8], PayheroChannelID: &channel}
		if err := models.Properties.Insert(ctx, tenant, property); err != nil {
			t.Fatal(err)
		}
		unit := &data.Unit{PropertyID: property.ID, UnitCode: unitCode, Status: "vacant"}
		if err := models.Units.Insert(ctx, tenant, unit); err != nil {
			t.Fatal(err)
		}
		lease := &data.Lease{UnitID: unit.ID, TenantName: "TENANT " + unitCode, PrimaryPhone: phone, RentAmount: money("5000"),
			StartDate: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), Status: "active"}
		if err := models.Leases.Insert(ctx, tenant, tenant, lease); err != nil {
			t.Fatal(err)
		}
		if err := models.Rent.PostManualCharge(ctx, tenant, tenant, unit.ID, data.LedgerTypeRent, money("5000"), "seed"); err != nil {
			t.Fatal(err)
		}
		target := data.PayTarget{TenantID: tenant, PropertyID: property.ID, UnitID: unit.ID}
		intent, err := models.PayAccess.CreateIntent(ctx, target, phone,
			[]data.IntentLine{{Type: data.LedgerTypeRent, Amount: money("5000")}}, 10*time.Minute)
		if err != nil {
			t.Fatalf("creating an intent for %s: %v", unitCode, err)
		}
		return firm{tenant: tenant, phone: phone, target: target, intentID: intent.ID}
	}
	a, b := newFirm("A1", "+254722000001"), newFirm("B1", "+254733000002")

	app := &application{logger: slog.New(slog.NewTextHandler(io.Discard, nil)), models: models}
	app.config.env = "development"
	app.config.pay.sessionSecret = testPaySecret
	app.config.pay.sessionTTL = 15 * time.Minute
	router, err := app.routes()
	if err != nil {
		t.Fatal(err)
	}
	var lastBody string
	status := func(f firm, intent uuid.UUID) int {
		// The lease is whatever is live for the unit under the token's own
		// tenant; a forged tenant/unit pairing has none and gets a random one.
		lease, err := models.PayAccess.PayLeaseID(ctx, f.tenant, f.target.UnitID, f.phone)
		if err != nil {
			lease = uuid.New()
		}
		tok, err := app.signPaySession(paySession{TenantID: f.tenant, UnitID: f.target.UnitID, LeaseID: lease, Phone: f.phone}, time.Now().Add(time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodGet, "/v1/pay/intents/"+intent.String()+"/status", nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		lastBody = w.Body.String()
		return w.Code
	}
	// leaks fails the test if the last response said anything about unit A: its
	// code, property, tenant, phone, intent id or amount.
	leaks := func(what string) {
		t.Helper()
		for _, secret := range []string{"A1", "Court A1", "TENANT A1", a.phone, a.intentID.String(), "5000"} {
			if strings.Contains(lastBody, secret) {
				t.Errorf("%s: response leaks %q: %s", what, secret, lastBody)
			}
		}
	}

	if code := status(a, a.intentID); code != http.StatusOK {
		t.Fatalf("control: a firm reading its own unit's intent = %d, want 200", code)
	}
	if code := status(b, a.intentID); code != http.StatusNotFound {
		t.Errorf("firm B reading firm A's intent = %d, want 404 (not 403: its existence is not confirmed)", code)
	}
	leaks("firm B reading firm A's intent")
	if code := status(a, b.intentID); code != http.StatusNotFound {
		t.Errorf("firm A reading firm B's intent = %d, want 404", code)
	}

	// A session minted for B's tenant but naming A's unit still finds nothing:
	// RLS hides A's rows from B's transaction whatever the unit id says.
	// It has no live lease under B's tenant, so it is refused outright (401);
	// either way it never reads A's intent.
	forged := firm{tenant: b.tenant, phone: a.phone, target: a.target}
	if code := status(forged, a.intentID); code != http.StatusNotFound && code != http.StatusUnauthorized {
		t.Errorf("B's tenant with A's unit id = %d, want 404 or 401", code)
	}
	leaks("forged token")

	// An unknown id is indistinguishable from someone else's.
	if code := status(a, uuid.New()); code != http.StatusNotFound {
		t.Errorf("unknown intent id = %d, want 404", code)
	}
}
