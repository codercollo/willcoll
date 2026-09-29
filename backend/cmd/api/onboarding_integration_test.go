package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/alexedwards/scs/v2/memstore"
	"github.com/codercollo/willcoll/backend/internal/data"
	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// TestOnboardingImportEndToEnd brings a building in through the HTTP API and
// checks what the manager would see afterwards: balances, the ledger, the
// report, and that isolation and refusals behave.
func TestOnboardingImportEndToEnd(t *testing.T) {
	appDSN, adminDSN := os.Getenv("WILLCOLL_TEST_DB_DSN"), os.Getenv("WILLCOLL_TEST_ADMIN_DB_DSN")
	if appDSN == "" || adminDSN == "" {
		t.Skip("WILLCOLL_TEST_DB_DSN and WILLCOLL_TEST_ADMIN_DB_DSN not set; skipping")
	}
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
	conn, adminConn := open(appDSN), open(adminDSN)
	models := data.NewModels(conn, adminConn, 5*time.Second)
	ctx := context.Background()

	newFirm := func() (uuid.UUID, string) {
		email := uuid.NewString()[:8] + "@ob.example.com"
		m := &data.Manager{FirmName: "Onboard Firm", Username: "u" + uuid.NewString()[:10], Email: email, Phone: "+254700000000", Status: data.ManagerStatusActive}
		if err := m.Password.Set("correct-horse-battery"); err != nil {
			t.Fatal(err)
		}
		if err := models.Managers.Insert(ctx, m); err != nil {
			t.Fatal(err)
		}
		now := time.Now()
		m.ActivatedAt = &now
		if err := models.Managers.Update(ctx, m); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { conn.Exec(`DELETE FROM managers WHERE id = $1`, m.ID) })
		return m.ID, email
	}
	tenant, email := newFirm()
	_, otherEmail := newFirm()

	landlord := uuid.New()
	tenantSQL(t, conn, tenant, func(tx *sql.Tx) {
		if _, err := tx.Exec(`INSERT INTO landlords (id, tenant_id, name, phone) VALUES ($1, $2, 'L', '+254711111111')`, landlord, tenant); err != nil {
			t.Fatal(err)
		}
	})
	property := &data.Property{LandlordID: landlord, Name: "Runda Arcade", Location: "Runda", Slug: "ob-" + uuid.NewString()[:8],
		GarbageEnabled: true, GarbageFee: money("300"), WaterRatePerUnit: money("150")}
	if err := models.Properties.Insert(ctx, tenant, property); err != nil {
		t.Fatal(err)
	}

	sm := scs.New()
	sm.Store = memstore.New()
	app := &application{logger: slog.New(slog.NewTextHandler(io.Discard, nil)), models: models, sessionManager: sm}
	app.config.env = "development"
	router, err := app.routes()
	if err != nil {
		t.Fatal(err)
	}
	call := func(method, path, body, cookie string) (int, map[string]json.RawMessage) {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "text/csv")
		if cookie != "" {
			req.Header.Set("Cookie", cookie)
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		var out map[string]json.RawMessage
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		if c := w.Result().Cookies(); len(c) > 0 && out != nil {
			out["_cookie"], _ = json.Marshal(c[0].Name + "=" + c[0].Value)
		}
		return w.Code, out
	}
	login := func(email string) string {
		req := httptest.NewRequest(http.MethodPost, "/v1/sessions", strings.NewReader(`{"email":"`+email+`","password":"correct-horse-battery"}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if w.Code != http.StatusOK || len(w.Result().Cookies()) == 0 {
			t.Fatalf("login: %d %s", w.Code, w.Body)
		}
		c := w.Result().Cookies()[0]
		return c.Name + "=" + c.Value
	}
	cookie, otherCookie := login(email), login(otherEmail)
	path := "/v1/properties/" + property.ID.String() + "/onboarding/import"

	sheet := "unit_code,meter_number,tenant_name,phone,rent,start_date,rent_deposit,water_deposit,rent_arrears,water_arrears,garbage_arrears,co_payer_name,co_payer_phone\n" +
		"G1,W-1,JOHN KAMAU,0722000001,\"18,000\",01/07/2025,36000,3000,5000,700,300,MARY WANJIKU,0733000002\n" +
		"G2,W-2,GRACE NJERI,0722000003,15000,2025-08-01,30000,3000,0,0,0,,\n" +
		"SHOP NO.3,,,,,,,,,,,,\n"

	// --- Signed out, and another firm's property.
	if code, _ := call(http.MethodPost, path, sheet, ""); code != http.StatusUnauthorized {
		t.Errorf("signed out: %d, want 401", code)
	}
	if code, _ := call(http.MethodPost, path, sheet, otherCookie); code != http.StatusNotFound {
		t.Errorf("another firm's property: %d, want 404", code)
	}

	// --- A bad date parameter and a bad sheet are refused with reasons.
	if code, body := call(http.MethodPost, path+"?as_at=tomorrow", sheet, cookie); code != http.StatusUnprocessableEntity || !strings.Contains(string(body["error"]), "as_at") {
		t.Errorf("bad as_at: %d %s", code, body["error"])
	}
	badSheet := "unit_code,tenant_name,phone,rent\nG1,JOHN,12345,0\n"
	code, body := call(http.MethodPost, path, badSheet, cookie)
	var rejected struct {
		Message string `json:"message"`
		Rows    []struct {
			Row   int    `json:"row"`
			Field string `json:"field"`
		} `json:"rows"`
	}
	_ = json.Unmarshal(body["error"], &rejected)
	if code != http.StatusUnprocessableEntity || len(rejected.Rows) < 2 || !strings.Contains(rejected.Message, "nothing was imported") {
		t.Fatalf("a bad sheet: %d %s", code, body["error"])
	}

	// --- Dry run: what would happen, and nothing written.
	code, body = call(http.MethodPost, path+"?dry_run=true&as_at=2026-10-01", sheet, cookie)
	if code != http.StatusOK {
		t.Fatalf("dry run: %d %s", code, body["error"])
	}
	var dry struct {
		DryRun  bool `json:"dry_run"`
		Summary struct {
			UnitsCreated  int    `json:"units_created"`
			LeasesCreated int    `json:"leases_created"`
			VacantUnits   int    `json:"vacant_units"`
			MonthlyRent   string `json:"monthly_rent"`
			RentArrears   string `json:"rent_arrears"`
		} `json:"summary"`
	}
	raw, _ := json.Marshal(body)
	_ = json.Unmarshal(raw, &dry)
	if !dry.DryRun || dry.Summary.UnitsCreated != 3 || dry.Summary.LeasesCreated != 2 || dry.Summary.VacantUnits != 1 || dry.Summary.MonthlyRent != "33000.00" || dry.Summary.RentArrears != "5000.00" {
		t.Errorf("dry run = %+v", dry)
	}
	if code, body := call(http.MethodGet, "/v1/properties/"+property.ID.String()+"/units", "", cookie); code != http.StatusOK || strings.Contains(string(body["units"]), "G1") {
		t.Errorf("a dry run created units: %d %s", code, body["units"])
	}

	// --- The real import.
	if code, body := call(http.MethodPost, path+"?as_at=2026-10-01", sheet, cookie); code != http.StatusCreated {
		t.Fatalf("import: %d %s", code, body["error"])
	}
	var g1 uuid.UUID
	tenantSQL(t, conn, tenant, func(tx *sql.Tx) {
		if err := tx.QueryRow(`SELECT id FROM units WHERE property_id = $1 AND unit_code = 'G1'`, property.ID).Scan(&g1); err != nil {
			t.Fatal(err)
		}
	})
	ledger := func(kind string) (bal string, entries int) {
		code, body := call(http.MethodGet, "/v1/units/"+g1.String()+"/ledger/"+kind, "", cookie)
		if code != http.StatusOK {
			t.Fatalf("ledger %s: %d", kind, code)
		}
		var b struct{ Balance struct{ Balance string } }
		_ = json.Unmarshal(mustMarshal(body), &b)
		var es []json.RawMessage
		_ = json.Unmarshal(body["entries"], &es)
		return b.Balance.Balance, len(es)
	}
	for kind, want := range map[string]string{"rent": "5000.00", "water": "700.00", "garbage": "300.00", "rent_deposit": "0.00", "water_deposit": "0.00"} {
		if bal, _ := ledger(kind); bal != want {
			t.Errorf("G1 %s balance = %s, want %s", kind, bal, want)
		}
	}
	if _, n := ledger("rent_deposit"); n != 2 {
		t.Errorf("the deposit ledger has %d entries, want the deposit and its opening credit", n)
	}

	// --- The rent overview shows the arrears owed; the report shows no money received.
	code, body = call(http.MethodGet, "/v1/properties/"+property.ID.String()+"/rent/overview?period=2026-10", "", cookie)
	if code != http.StatusOK || !strings.Contains(string(body["overview"]), "5000.00") {
		t.Errorf("rent overview: %d %s", code, body["overview"])
	}
	code, body = call(http.MethodGet, "/v1/properties/"+property.ID.String()+"/reports/2026-10", "", cookie)
	var rep struct {
		Report struct {
			Totals struct {
				GrandTotal string `json:"grand_total"`
			}
		}
	}
	_ = json.Unmarshal(mustMarshal(body), &rep)
	if code != http.StatusOK || rep.Report.Totals.GrandTotal != "0.00" {
		t.Errorf("the report after onboarding: %d grand total %q, want 0.00 (no money was received)", code, rep.Report.Totals.GrandTotal)
	}

	// --- The setup checklist moves on: tenants are in, M-Pesa is next.
	code, body = call(http.MethodGet, "/v1/onboarding", "", cookie)
	var ob struct {
		Onboarding struct {
			Complete bool   `json:"complete"`
			Next     string `json:"next"`
			Done     int    `json:"done"`
		} `json:"onboarding"`
	}
	_ = json.Unmarshal(mustMarshal(body), &ob)
	if code != http.StatusOK || ob.Onboarding.Complete || ob.Onboarding.Next != "mpesa" || ob.Onboarding.Done != 3 {
		t.Errorf("checklist after the import: %d %+v, want tenants done and mpesa next", code, ob.Onboarding)
	}
	if code, _ := call(http.MethodGet, "/v1/onboarding", "", ""); code != http.StatusUnauthorized {
		t.Errorf("checklist signed out: %d, want 401", code)
	}

	// --- The same file again is refused: those units now have tenants.
	if code, _ := call(http.MethodPost, path, sheet, cookie); code != http.StatusUnprocessableEntity {
		t.Errorf("re-import: %d, want 422", code)
	}
}

func mustMarshal(v any) []byte { b, _ := json.Marshal(v); return b }
