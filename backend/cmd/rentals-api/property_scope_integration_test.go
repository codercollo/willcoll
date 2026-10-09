package main

import (
	"database/sql"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	platformdata "github.com/codercollo/willcoll/backend/internal/platform/data"
	platformsqlc "github.com/codercollo/willcoll/backend/internal/platform/db/sqlc"
	"github.com/codercollo/willcoll/backend/internal/rentals/data"
	"github.com/codercollo/willcoll/backend/internal/testenv"
	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// Property scope end to end (docs/security/authorization.md D2, C2): a staff
// member restricted to property A, through the real router and the real
// database. Everything of property B must be absent: lists, dashboards,
// exports, and every route that names a B resource answers 404.

type scopeHTTP struct {
	router http.Handler
	app    *application
	stub   *platformStub
	conn   *sql.DB
	models data.Models

	firm, otherFirm uuid.UUID

	landlordA, landlordB uuid.UUID
	propA, propB         *data.Property
	unitA, unitB         *data.Unit
	leaseA, leaseB       *data.Lease
	paymentB             uuid.UUID
	entryB               uuid.UUID

	// Only the route matrix seeds these (matrixReseed): a co-payer on lease B, a
	// pending cash payment on unit B1, and property B's payment channel.
	payerB   *data.LeasePayer
	pendingB uuid.UUID
	channelB string
}

func newScopeHTTP(t *testing.T) *scopeHTTP { return newScopeHTTPMarked(t, false) }

// newScopeHTTPMarked is newScopeHTTP with the handler-reached marker on or off
// (auth.MarkReached): only the route matrix needs it on.
func newScopeHTTPMarked(t *testing.T, markHandlers bool) *scopeHTTP {
	t.Helper()
	dsns := testenv.Require(t, "WILLCOLL_TEST_DB_DSN", "WILLCOLL_TEST_ADMIN_DB_DSN")
	open := func(dsn string) *sql.DB {
		d, err := sql.Open("pgx", dsn)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { d.Close() })
		if err := d.Ping(); err != nil {
			t.Fatal(err)
		}
		return d
	}
	conn, adminConn := open(dsns[0]), open(dsns[1])
	models := data.NewModels(conn, adminConn, 5*time.Second)

	s := &scopeHTTP{conn: conn, stub: newPlatformStub(t), models: models}
	s.reseed(t)

	app := &application{logger: slog.New(slog.NewTextHandler(io.Discard, nil)), models: models, payhero: &fakePush{}, sms: &fakeSMS{}, markHandlers: markHandlers}
	s.stub.attach(app)
	app.config.env = "development"
	app.config.pay.sessionSecret = testPaySecret
	app.config.pay.otpTTL = 5 * time.Minute
	app.config.pay.sessionTTL = 15 * time.Minute
	app.config.pay.intentTTL = 10 * time.Minute
	router, err := app.routes()
	if err != nil {
		t.Fatal(err)
	}
	s.app, s.router = app, router
	return s
}

// reseed seeds a brand-new fixture: two firms, and for the first firm two
// landlords, two properties (A and B) each with a unit, an active lease, a
// posted charge, and for B a payment and its ledger entry. The previous
// fixture's rows stay where they are, under their own firm ids, so a test (the
// route matrix) can call a destructive route against pristine data every time.
func (s *scopeHTTP) reseed(t *testing.T) {
	t.Helper()
	models, ctx, conn := s.models, t.Context(), s.conn
	s.firm, s.otherFirm = s.stub.newFirm(), s.stub.newFirm()

	landlord := func(name string) uuid.UUID {
		l := &data.Landlord{Name: name, Phone: "+254711111111"}
		if err := models.Landlords.Insert(ctx, s.firm, l); err != nil {
			t.Fatal(err)
		}
		return l.ID
	}
	s.landlordA, s.landlordB = landlord("Landlord A"), landlord("Landlord B")

	seed := func(name string, landlordID uuid.UUID, code string) (*data.Property, *data.Unit, *data.Lease) {
		p := &data.Property{LandlordID: landlordID, Name: name, Location: "Nairobi", Slug: "hs-" + uuid.NewString()[:8],
			WaterRatePerUnit: money("150"), ManagementFeePercent: 5, ElectricityEnabled: true, ElectricityDepositAmount: money("1000")}
		if err := models.Properties.Insert(ctx, s.firm, p); err != nil {
			t.Fatal(err)
		}
		u := &data.Unit{PropertyID: p.ID, UnitCode: code, Status: "vacant"}
		if err := models.Units.Insert(ctx, s.firm, u); err != nil {
			t.Fatal(err)
		}
		l := &data.Lease{UnitID: u.ID, TenantName: "TENANT " + code, PrimaryPhone: "+254700000000", RentAmount: money("10000"),
			StartDate: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), Status: "active"}
		if err := models.Leases.Insert(ctx, s.firm, s.firm, l); err != nil {
			t.Fatal(err)
		}
		if _, err := models.Leases.EnableElectricityDeposit(ctx, s.firm, s.firm, p.ID, money("1000")); err != nil {
			t.Fatal(err)
		}
		if err := models.Rent.PostManualCharge(ctx, s.firm, s.firm, u.ID, data.LedgerTypeRent, money("10000"), "rent"); err != nil {
			t.Fatal(err)
		}
		return p, u, l
	}
	s.propA, s.unitA, s.leaseA = seed("Property Alpha", s.landlordA, "A1")
	s.propB, s.unitB, s.leaseB = seed("Property Bravo", s.landlordB, "B1")

	pay, err := models.Rent.PostPayment(ctx, s.firm, s.firm, s.unitB.ID, data.PaymentInput{Amount: money("2500"), Source: "manual", Reference: "HB-1"})
	if err != nil {
		t.Fatal(err)
	}
	s.paymentB = pay.PaymentID
	tenantSQL(t, conn, s.firm, func(tx *sql.Tx) {
		if err := tx.QueryRow(`SELECT le.id FROM ledger_entries le JOIN ledger_accounts la ON la.id = le.ledger_account_id
			WHERE la.unit_id = $1 LIMIT 1`, s.unitB.ID).Scan(&s.entryB); err != nil {
			t.Fatal(err)
		}
	})
}

func (s *scopeHTTP) do(method, target, cookie string) (int, string) {
	var body io.Reader
	if method != http.MethodGet && method != http.MethodDelete {
		body = strings.NewReader(`{}`)
	}
	req := httptest.NewRequest(method, target, body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Cookie", cookie)
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)
	return w.Code, w.Body.String()
}

func onlyA(t *testing.T, what, body string, s *scopeHTTP) {
	t.Helper()
	if !strings.Contains(body, s.propA.ID.String()) && !strings.Contains(body, "A1") && !strings.Contains(body, s.landlordA.String()) {
		t.Errorf("%s: shows nothing of property A: %s", what, body)
	}
	for _, leak := range []string{s.propB.ID.String(), s.unitB.ID.String(), s.landlordB.String(), "B1", "Bravo", "TENANT B1"} {
		if strings.Contains(body, leak) {
			t.Errorf("%s: leaks %q of property B: %s", what, leak, body)
		}
	}
}

func TestPropertyScopeHTTP_ListsDashboardsAndExports(t *testing.T) {
	s := newScopeHTTP(t)
	_, cookie := s.stub.newStaff(t, s.firm, platformdata.RoleAccountant, map[string][]uuid.UUID{"rentals": {s.propA.ID}})

	for _, tc := range []struct{ name, path string }{
		{"property list", "/v1/properties"},
		{"landlord list", "/v1/landlords"},
		{"property detail", "/v1/properties/" + s.propA.ID.String()},
		{"units of A", "/v1/properties/" + s.propA.ID.String() + "/units"},
		{"rent overview of A", "/v1/properties/" + s.propA.ID.String() + "/rent/overview?period=2026-09"},
		{"payment review queue", "/v1/payments/review"},
		{"electricity deposits json", "/v1/properties/" + s.propA.ID.String() + "/electricity/deposits"},
		{"electricity deposits csv export", "/v1/properties/" + s.propA.ID.String() + "/electricity/deposits.csv"},
		{"onboarding", "/v1/onboarding"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, body := s.do(http.MethodGet, tc.path, cookie)
			if code != http.StatusOK {
				t.Fatalf("status %d, want 200: %s", code, body)
			}
			if tc.name != "payment review queue" && tc.name != "onboarding" {
				onlyA(t, tc.name, body, s)
			}
			for _, leak := range []string{s.propB.ID.String(), "Bravo", "TENANT B1"} {
				if strings.Contains(body, leak) {
					t.Errorf("%s leaks %q: %s", tc.name, leak, body)
				}
			}
		})
	}

	// The same lists for B's resources: 404, never an empty 200 that hints they exist.
	for _, path := range []string{
		"/v1/properties/" + s.propB.ID.String(),
		"/v1/properties/" + s.propB.ID.String() + "/units",
		"/v1/properties/" + s.propB.ID.String() + "/rent/overview?period=2026-09",
		"/v1/properties/" + s.propB.ID.String() + "/electricity/deposits.csv",
		"/v1/properties/" + s.propB.ID.String() + "/electricity/deposits.pdf",
		"/v1/units/" + s.unitB.ID.String() + "/ledger/RENT",
	} {
		if code, body := s.do(http.MethodGet, path, cookie); code != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404: %s", path, code, body)
		}
	}

	// A restricted member cannot create firm-wide records.
	for _, path := range []string{"/v1/properties", "/v1/landlords"} {
		if code, _ := s.do(http.MethodPost, path, cookie); code != http.StatusForbidden {
			t.Errorf("POST %s = %d, want 403", path, code)
		}
	}
}

// Every route that names a resource in its path answers 404 for a resource of
// property B, whatever its method and whatever permission the caller holds.
func TestPropertyScopeHTTP_EveryResourceRouteIs404ForOutOfScope(t *testing.T) {
	s := newScopeHTTP(t)
	_, cookie := s.stub.newStaff(t, s.firm, platformdata.RoleAccountant, map[string][]uuid.UUID{"rentals": {s.propA.ID}})

	m := newMiddlewareTestApp(t) // only to list the registered routes
	routes := m.router.Routes()

	idFor := map[data.ScopeKind]uuid.UUID{
		data.ScopeProperty: s.propB.ID, data.ScopeUnit: s.unitB.ID, data.ScopeLease: s.leaseB.ID,
		data.ScopeLandlord: s.landlordB, data.ScopePayment: s.paymentB, data.ScopeLedgerEntry: s.entryB,
	}
	var swept []string
	for _, r := range routes {
		if !strings.HasPrefix(r.Path, "/v1/") {
			continue
		}
		kind, ok := scopeKindForRoute(r.Path)
		if !ok {
			continue
		}
		path := strings.NewReplacer(":id", idFor[kind].String(), ":period", "2026-08.pdf", ":type", "RENT", ":payerId", uuid.NewString()).Replace(r.Path)
		code, body := s.do(r.Method, path, cookie)
		swept = append(swept, r.Method+" "+r.Path)
		if code != http.StatusNotFound {
			t.Errorf("%s %s (B's %s) = %d, want 404: %s", r.Method, r.Path, kind, code, body)
		}
	}
	if len(swept) < 40 {
		t.Fatalf("swept only %d resource routes; the route table changed shape: %v", len(swept), swept)
	}
	sort.Strings(swept)
	t.Logf("swept %d resource routes", len(swept))
}

// The same ids are fine for callers who are not restricted, and a restricted
// caller keeps working on their own property.
func TestPropertyScopeHTTP_UnrestrictedAndOwnProperty(t *testing.T) {
	s := newScopeHTTP(t)
	scopedID, scoped := s.stub.newStaff(t, s.firm, platformdata.RoleAccountant, map[string][]uuid.UUID{"rentals": {s.propA.ID}})
	_, noList := s.stub.newStaff(t, s.firm, platformdata.RoleAccountant, nil)
	_, emptyList := s.stub.newStaff(t, s.firm, platformdata.RoleAccountant, map[string][]uuid.UUID{"rentals": {}})
	_, ownerWithStaleList := s.stub.newStaff(t, s.firm, platformdata.RoleOwner, map[string][]uuid.UUID{"rentals": {s.propA.ID}})
	firmLogin := s.stub.loginManager(t, s.firm)

	bravo := "/v1/properties/" + s.propB.ID.String()
	for name, cookie := range map[string]string{"no list": noList, "empty list": emptyList, "owner (list ignored)": ownerWithStaleList, "firm login": firmLogin} {
		if code, body := s.do(http.MethodGet, bravo, cookie); code != http.StatusOK || !strings.Contains(body, "Bravo") {
			t.Errorf("%s: GET B = %d, want 200 with B: %s", name, code, body)
		}
		if code, body := s.do(http.MethodGet, "/v1/properties", cookie); code != http.StatusOK ||
			!strings.Contains(body, "Alpha") || !strings.Contains(body, "Bravo") {
			t.Errorf("%s: list = %d, want both properties: %s", name, code, body)
		}
	}

	for _, path := range []string{
		"/v1/properties/" + s.propA.ID.String(), "/v1/units/" + s.unitA.ID.String(),
		"/v1/units/" + s.unitA.ID.String() + "/leases", "/v1/leases/" + s.leaseA.ID.String(),
		"/v1/units/" + s.unitA.ID.String() + "/ledger/RENT", "/v1/landlords/" + s.landlordA.String(),
	} {
		if code, body := s.do(http.MethodGet, path, scoped); code != http.StatusOK {
			t.Errorf("scoped GET %s = %d, want 200: %s", path, code, body)
		}
	}

	// A scope is read on every request: narrowing it to B takes effect on the
	// very next call of the same session, with no re-login.
	s.stub.editStaff(scopedID, func(st *platformsqlc.Staff) {
		st.PropertyAccess = []byte(fmt.Sprintf(`{"rentals":["%s"]}`, s.propB.ID))
	})
	if code, _ := s.do(http.MethodGet, "/v1/properties/"+s.propA.ID.String(), scoped); code != http.StatusNotFound {
		t.Errorf("after re-scoping to B, GET A = %d, want 404", code)
	}
	if code, _ := s.do(http.MethodGet, bravo, scoped); code != http.StatusOK {
		t.Errorf("after re-scoping to B, GET B = %d, want 200", code)
	}
}

// Firm isolation stays the outer wall: a scope list naming another firm's
// property reveals nothing, and another firm cannot reach this firm's rows.
func TestPropertyScopeHTTP_CrossTenant(t *testing.T) {
	s := newScopeHTTP(t)
	foreign := "/v1/properties/" + s.propA.ID.String()

	otherOwner := s.stub.loginManager(t, s.otherFirm)
	if code, _ := s.do(http.MethodGet, foreign, otherOwner); code != http.StatusNotFound {
		t.Errorf("another firm's login reading this firm's property = %d, want 404", code)
	}
	_, otherScoped := s.stub.newStaff(t, s.otherFirm, platformdata.RoleAccountant, map[string][]uuid.UUID{"rentals": {s.propA.ID}})
	if code, _ := s.do(http.MethodGet, foreign, otherScoped); code != http.StatusNotFound {
		t.Errorf("another firm's scoped staff with this firm's property in their list = %d, want 404", code)
	}
	if code, body := s.do(http.MethodGet, "/v1/properties", otherScoped); code != http.StatusOK || strings.Contains(body, "Alpha") {
		t.Errorf("another firm's list = %d: %s", code, body)
	}
}

// A role change or disabling applies on the next request too (the platform
// side also ends the sessions; see cmd/platform-api).
func TestPropertyScopeHTTP_DemotionAndDisableApplyImmediately(t *testing.T) {
	s := newScopeHTTP(t)
	id, cookie := s.stub.newStaff(t, s.firm, platformdata.RoleAccountant, map[string][]uuid.UUID{"rentals": {s.propA.ID}})
	reverse := "/v1/ledger-entries/" + uuid.NewString() + "/reverse"

	if code, _ := s.do(http.MethodPost, reverse, cookie); code == http.StatusForbidden {
		t.Fatalf("accountant POST reversal = 403 before demotion")
	}
	s.stub.editStaff(id, func(st *platformsqlc.Staff) { st.Role = platformdata.RoleViewer })
	if code, _ := s.do(http.MethodPost, reverse, cookie); code != http.StatusNotFound && code != http.StatusForbidden {
		t.Errorf("demoted to viewer: POST reversal = %d", code)
	}
	if code, _ := s.do(http.MethodPost, "/v1/units/"+s.unitA.ID.String()+"/rent/payments", cookie); code != http.StatusForbidden {
		t.Errorf("demoted to viewer: POST payment = %d, want 403", code)
	}
	s.stub.editStaff(id, func(st *platformsqlc.Staff) { st.Status = platformdata.StaffDisabled })
	if code, _ := s.do(http.MethodGet, "/v1/properties", cookie); code != http.StatusUnauthorized {
		t.Errorf("disabled: GET = %d, want 401", code)
	}
}
