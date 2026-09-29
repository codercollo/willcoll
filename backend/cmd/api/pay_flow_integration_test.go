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

// TestTenantPaymentFlowEndToEnd drives the public pay surface and the PayHero
// webhook through the real router and a real database, with only PayHero and
// SMS faked: a tenant proves their phone by SMS code, sees what they owe,
// requests an STK push, PayHero calls back, and the ledger, the status poll
// and the confirmation text all agree. Redelivering the callback changes
// nothing.
func TestTenantPaymentFlowEndToEnd(t *testing.T) {
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

	// --- Seed a manager, a property on its own PayHero channel, and one unit.
	tenant := uuid.New()
	if _, err := conn.Exec(`INSERT INTO managers (id, firm_name, username, email, phone, password_hash)
		VALUES ($1, 'E2E Managers', $2, $3, '+254700000000', '\x00')`, tenant, "u-"+tenant.String(), tenant.String()+"@example.com"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Exec(`DELETE FROM managers WHERE id = $1`, tenant) })
	landlord := uuid.New()
	tenantSQL(t, conn, tenant, func(tx *sql.Tx) {
		if _, err := tx.Exec(`INSERT INTO landlords (id, tenant_id, name, phone) VALUES ($1, $2, 'L', '+254711111111')`, landlord, tenant); err != nil {
			t.Fatal(err)
		}
	})
	channel := "CH-" + uuid.NewString()[:8]
	slug := "e2e-" + uuid.NewString()[:8]
	property := &data.Property{LandlordID: landlord, Name: "E2E Court", Location: "Nairobi", Slug: slug,
		GarbageEnabled: false, PayheroChannelID: &channel}
	if err := models.Properties.Insert(ctx, tenant, property); err != nil {
		t.Fatal(err)
	}
	unit := &data.Unit{PropertyID: property.ID, UnitCode: "A1", Status: "vacant"}
	if err := models.Units.Insert(ctx, tenant, unit); err != nil {
		t.Fatal(err)
	}
	lease := &data.Lease{UnitID: unit.ID, TenantName: "JOHN KAMAU", PrimaryPhone: "+254722000001", RentAmount: money("5000"),
		StartDate: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), Status: "active"}
	if err := models.Leases.Insert(ctx, tenant, tenant, lease); err != nil {
		t.Fatal(err)
	}
	for ledger, amount := range map[string]string{data.LedgerTypeRent: "5000", data.LedgerTypeWater: "1000"} {
		if err := models.Rent.PostManualCharge(ctx, tenant, tenant, unit.ID, ledger, money(amount), "seed"); err != nil {
			t.Fatal(err)
		}
	}
	balance := func(ledger string) string {
		var b string
		tenantSQL(t, conn, tenant, func(tx *sql.Tx) {
			if err := tx.QueryRow(`SELECT COALESCE((SELECT balance::text FROM unit_ledger_balances WHERE unit_id = $1 AND type = $2), '0')`, unit.ID, ledger).Scan(&b); err != nil {
				t.Fatal(err)
			}
		})
		return b
	}

	// --- The real router, with PayHero and SMS faked.
	push, texts := &fakePush{}, &fakeSMS{}
	sm := scs.New()
	sm.Store = memstore.New()
	app := &application{
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)), models: models, sessionManager: sm,
		payhero: push, sms: texts,
	}
	app.config.env = "development"
	app.config.payhero.webhookSecret = testWebhookSecret
	app.config.payhero.callbackBaseURL = "https://api.example.com"
	app.config.pay.sessionSecret = testPaySecret
	app.config.pay.otpTTL, app.config.pay.sessionTTL, app.config.pay.intentTTL = 5*time.Minute, 15*time.Minute, 10*time.Minute
	router, err := app.routes()
	if err != nil {
		t.Fatal(err)
	}
	call := func(method, path, body string, headers map[string]string) (int, map[string]json.RawMessage) {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		var out map[string]json.RawMessage
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		return w.Code, out
	}
	base := "/v1/pay/" + slug + "/A1"

	// 1. The public page shows the unit, and nothing about who lives there.
	code, out := call("GET", base, "", nil)
	if code != http.StatusOK || !strings.Contains(string(out["unit"]), `"unit_code": "A1"`) && !strings.Contains(string(out["unit"]), `"unit_code":"A1"`) {
		t.Fatalf("pay page = %d %s", code, out["unit"])
	}
	if strings.Contains(string(out["unit"]), "KAMAU") {
		t.Error("the public page leaked the tenant name")
	}

	// 2. Balances are closed until the phone is proved.
	if code, _ := call("GET", base+"/balances", "", nil); code != http.StatusUnauthorized {
		t.Fatalf("balances without a session = %d, want 401", code)
	}

	// 3. A stranger's number gets the same answer and no text; the tenant's gets a code.
	call("POST", base+"/otp", `{"phone":"0799000000"}`, nil)
	app.wg.Wait()
	if len(texts.otps) != 0 {
		t.Fatal("a code was texted to a number not on the lease")
	}
	if code, _ := call("POST", base+"/otp", `{"phone":"0722 000 001"}`, nil); code != http.StatusAccepted {
		t.Fatalf("otp request = %d", code)
	}
	app.wg.Wait()
	if len(texts.otps) != 1 {
		t.Fatalf("texts = %d, want 1", len(texts.otps))
	}
	otp := texts.otps[0]

	// 4. A wrong code is refused; the right one gives a session.
	if code, _ := call("POST", base+"/otp/verify", `{"phone":"0722000001","code":"000000"}`, nil); code != http.StatusUnauthorized && otp.Code != "000000" {
		t.Fatalf("wrong code = %d, want 401", code)
	}
	code, out = call("POST", base+"/otp/verify", `{"phone":"0722000001","code":"`+otp.Code+`"}`, nil)
	if code != http.StatusOK {
		t.Fatalf("verify = %d %v", code, out)
	}
	var token string
	_ = json.Unmarshal(out["token"], &token)
	auth := map[string]string{"Authorization": "Bearer " + token}
	if code, _ := call("POST", base+"/otp/verify", `{"phone":"0722000001","code":"`+otp.Code+`"}`, nil); code != http.StatusUnauthorized {
		t.Errorf("a code must work once, second use = %d", code)
	}

	// 5. Balances: rent and water owed; deposit lines always show (settled,
	// so "payable": false each), no garbage (not billed).
	code, out = call("GET", base+"/balances", "", auth)
	bal := string(out["balances"])
	if code != http.StatusOK || !strings.Contains(bal, "5000.00") || !strings.Contains(bal, "1000.00") ||
		!strings.Contains(bal, "RENT_DEPOSIT") || !strings.Contains(bal, "WATER_DEPOSIT") || strings.Contains(bal, "GARBAGE") {
		t.Fatalf("balances = %d %s", code, bal)
	}
	var balLines []struct {
		Type    string `json:"type"`
		Payable bool   `json:"payable"`
	}
	_ = json.Unmarshal(out["balances"], &balLines)
	for _, l := range balLines {
		if (l.Type == "RENT_DEPOSIT" || l.Type == "WATER_DEPOSIT") && l.Payable {
			t.Errorf("%s is settled and must not be payable", l.Type)
		}
	}

	// 6. The same session cannot be used for another unit's URL.
	if code, _ := call("GET", "/v1/pay/"+slug+"/NOPE/balances", "", auth); code != http.StatusNotFound {
		t.Errorf("unknown unit = %d, want 404", code)
	}

	// 7. Intent: PayHero is asked to prompt the phone, on the property channel.
	code, out = call("POST", base+"/intent", `{"lines":[{"type":"rent","amount":"5000"},{"type":"water","amount":"1000"}]}`, auth)
	if code != http.StatusAccepted {
		t.Fatalf("intent = %d %v", code, out)
	}
	if len(push.calls) != 1 || push.calls[0].Amount != 6000 || push.calls[0].ChannelID != channel || push.calls[0].PhoneNumber != "254722000001" {
		t.Fatalf("push = %+v", push.calls)
	}
	reference := push.calls[0].ExternalReference
	var intent struct {
		ID     uuid.UUID `json:"id"`
		Status string    `json:"status"`
	}
	_ = json.Unmarshal(out["intent"], &intent)
	statusPath := "/v1/pay/intents/" + intent.ID.String() + "/status"

	// 8. Still pending until the callback.
	if _, out := call("GET", statusPath, "", auth); !strings.Contains(string(out["intent"]), "pending") {
		t.Errorf("status before the callback = %s", out["intent"])
	}

	// 9. PayHero calls back (with the shared secret), and is acknowledged at once.
	callback := `{"response":{"Amount":6000,"CheckoutRequestID":"ws_CO_1","ExternalReference":"` + reference +
		`","MpesaReceiptNumber":"E2ERCPT01","Phone":"254722000001","ResultCode":0,"Status":"Success"}}`
	if code, _ := call("POST", "/v1/webhooks/payhero/collections", callback, nil); code != http.StatusUnauthorized {
		t.Fatalf("an unauthenticated callback = %d, want 401", code)
	}
	if code, _ := call("POST", "/v1/webhooks/payhero/collections?token="+testWebhookSecret, callback, nil); code != http.StatusOK {
		t.Fatalf("callback = %d", code)
	}
	app.wg.Wait()

	// 10. The ledger, the status poll and the text all agree.
	if r, w := balance(data.LedgerTypeRent), balance(data.LedgerTypeWater); r != "0.00" || w != "0.00" {
		t.Errorf("balances after payment: rent %s water %s, want both 0.00", r, w)
	}
	if _, out := call("GET", statusPath, "", auth); !strings.Contains(string(out["intent"]), "completed") {
		t.Errorf("status after the callback = %s, want completed", out["intent"])
	}
	// The tenant's itemised receipt comes with the completed status.
	_, out = call("GET", statusPath, "", auth)
	for _, want := range []string{"E2ERCPT01", "6000.00", "E2E Court", "A1", "RENT", "WATER", "1000.00", "5000.00"} {
		if !strings.Contains(string(out["intent"]), want) {
			t.Errorf("the completed status must carry a receipt with %q: %s", want, out["intent"])
		}
	}
	if len(texts.confirmations) != 1 || texts.confirmations[0].Amount != "6,000.00" || texts.confirmations[0].UnitCode != "A1" {
		t.Errorf("confirmations = %+v", texts.confirmations)
	}
	if code, out := call("GET", base+"/balances", "", auth); code != http.StatusOK || strings.Contains(string(out["balances"]), `"5000.00"`) {
		t.Errorf("balances still show rent owed after paying: %s", out["balances"])
	}

	// 11. PayHero retries the callback: nothing changes, nobody is texted twice.
	call("POST", "/v1/webhooks/payhero/collections?token="+testWebhookSecret, callback, nil)
	app.wg.Wait()
	if r := balance(data.LedgerTypeRent); r != "0.00" {
		t.Errorf("redelivery changed the rent balance to %s", r)
	}
	if len(texts.confirmations) != 1 {
		t.Errorf("redelivery texted the tenant again (%d texts)", len(texts.confirmations))
	}
	var payments int
	tenantSQL(t, conn, tenant, func(tx *sql.Tx) {
		_ = tx.QueryRow(`SELECT count(*) FROM payments WHERE mpesa_receipt = 'E2ERCPT01'`).Scan(&payments)
	})
	if payments != 1 {
		t.Errorf("payments rows for the receipt = %d, want 1", payments)
	}

	// 12. A cancelled push marks its intent failed, and the payer sees why.
	code, out = call("POST", base+"/intent", `{"lines":[{"type":"rent","amount":"100"}]}`, auth)
	if code != http.StatusAccepted {
		t.Fatalf("second intent = %d", code)
	}
	ref2 := push.calls[1].ExternalReference
	_ = json.Unmarshal(out["intent"], &intent)
	cancelled := `{"response":{"ExternalReference":"` + ref2 + `","ResultCode":1032,"ResultDesc":"Request cancelled by user","Status":"Failed"}}`
	call("POST", "/v1/webhooks/payhero/collections?token="+testWebhookSecret, cancelled, nil)
	app.wg.Wait()
	if _, out := call("GET", "/v1/pay/intents/"+intent.ID.String()+"/status", "", auth); !strings.Contains(string(out["intent"]), "failed") ||
		!strings.Contains(string(out["intent"]), "cancelled") {
		t.Errorf("status after a cancelled push = %s", out["intent"])
	}
}

func tenantSQL(t *testing.T, db *sql.DB, tenant uuid.UUID, fn func(tx *sql.Tx)) {
	t.Helper()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`SELECT set_config('app.tenant_id', $1, true)`, tenant.String()); err != nil {
		t.Fatal(err)
	}
	fn(tx)
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}
