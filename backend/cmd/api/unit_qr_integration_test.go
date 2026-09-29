package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"image/png"
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

// TestUnitQRCodesEndToEnd drives the QR routes through the real router and a
// real database: generation, the PNG, scans, rotation, lease termination and
// isolation between firms. Only sessions are in-memory.
func TestUnitQRCodesEndToEnd(t *testing.T) {
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

	// --- Two firms, each able to sign in.
	newFirm := func(name string) (id uuid.UUID, email string) {
		email = uuid.NewString()[:8] + "@qr.example.com"
		m := &data.Manager{FirmName: name, Username: "u" + uuid.NewString()[:10], Email: email, Phone: "+254700000000", Status: data.ManagerStatusActive}
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
	tenant, email := newFirm("QR Firm")
	_, otherEmail := newFirm("Other Firm")

	landlord := uuid.New()
	tenantSQL(t, conn, tenant, func(tx *sql.Tx) {
		if _, err := tx.Exec(`INSERT INTO landlords (id, tenant_id, name, phone) VALUES ($1, $2, 'L', '+254711111111')`, landlord, tenant); err != nil {
			t.Fatal(err)
		}
	})
	slug := "qr-" + uuid.NewString()[:8]
	property := &data.Property{LandlordID: landlord, Name: "QR Court", Location: "Nairobi", Slug: slug}
	if err := models.Properties.Insert(ctx, tenant, property); err != nil {
		t.Fatal(err)
	}
	unit := &data.Unit{PropertyID: property.ID, UnitCode: "A1", Status: "vacant"}
	if err := models.Units.Insert(ctx, tenant, unit); err != nil {
		t.Fatal(err)
	}
	lease := &data.Lease{UnitID: unit.ID, TenantName: "JOHN KAMAU", PrimaryPhone: "+254722000001", RentAmount: money("5000"),
		RentDepositAmount: money("5000"), StartDate: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), Status: "active"}
	if err := models.Leases.Insert(ctx, tenant, tenant, lease); err != nil {
		t.Fatal(err)
	}

	// --- The real router.
	sm := scs.New()
	sm.Store = memstore.New()
	app := &application{logger: slog.New(slog.NewTextHandler(io.Discard, nil)), models: models, sessionManager: sm}
	app.config.env = "development"
	router, err := app.routes()
	if err != nil {
		t.Fatal(err)
	}
	call := func(method, path, body, cookie string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if cookie != "" {
			req.Header.Set("Cookie", cookie)
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		return w
	}
	login := func(email string) string {
		w := call(http.MethodPost, "/v1/sessions", `{"email":"`+email+`","password":"correct-horse-battery"}`, "")
		if w.Code != http.StatusOK {
			t.Fatalf("login %s: %d %s", email, w.Code, w.Body)
		}
		for _, c := range w.Result().Cookies() {
			return c.Name + "=" + c.Value
		}
		t.Fatal("no session cookie")
		return ""
	}
	cookie, otherCookie := login(email), login(otherEmail)
	qrPath := "/v1/leases/" + lease.ID.String() + "/qr"

	type qrBody struct {
		QR struct {
			Token         string `json:"token"`
			URL           string `json:"url"`
			ShortCode     string `json:"short_code"`
			ScanCount     int    `json:"scan_count"`
			LastScannedAt string `json:"last_scanned_at"`
		} `json:"qr"`
	}
	getQR := func() qrBody {
		w := call(http.MethodGet, qrPath, "", cookie)
		if w.Code != http.StatusOK {
			t.Fatalf("GET qr: %d %s", w.Code, w.Body)
		}
		var b qrBody
		if err := json.Unmarshal(w.Body.Bytes(), &b); err != nil {
			t.Fatal(err)
		}
		return b
	}
	location := func(w *httptest.ResponseRecorder) string { return w.Header().Get("Location") }

	// --- Fails closed without QR_BASE_URL, exactly like the payment config.
	if w := call(http.MethodGet, qrPath, "", cookie); w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), "not available") {
		t.Errorf("no QR_BASE_URL: %d %s, want 503 not available", w.Code, w.Body)
	}
	if w := call(http.MethodGet, qrPath+".png", "", cookie); w.Code != http.StatusServiceUnavailable {
		t.Errorf("no QR_BASE_URL (png): %d", w.Code)
	}
	app.config.qr.baseURL = "https://willcoll.example"

	// --- Signed out: 401.
	if w := call(http.MethodGet, qrPath, "", ""); w.Code != http.StatusUnauthorized {
		t.Errorf("signed out: %d, want 401", w.Code)
	}

	// --- Generation: token, URL on the branded domain, grouped short code.
	// The current scheme's URL and short code both carry the unit's own
	// label ("A1-K7QM2XH9" / "A1-K7QM-2XH9") — by design (system-design.txt
	// Feature 1): the label alone identifies nothing about the tenant or
	// what they owe.
	first := getQR()
	wantScan, wantShort := data.FormatUnitQRCodes(first.QR.Token, "A1")
	if first.QR.URL != "https://willcoll.example/q/"+wantScan || first.QR.ShortCode != wantShort || first.QR.ScanCount != 0 {
		t.Fatalf("qr = %+v, want scan %q short %q", first.QR, wantScan, wantShort)
	}
	if again := getQR(); again.QR.Token != first.QR.Token {
		t.Errorf("GET qr twice gave two codes: %s vs %s", first.QR.Token, again.QR.Token)
	}
	// The QR must carry only the URL and unit label: nothing about the
	// tenant or the money involved is in what the sticker encodes.
	for _, secret := range []string{"JOHN", "5000", slug, "254722"} {
		if strings.Contains(first.QR.URL, secret) {
			t.Errorf("the QR URL %q leaks %q", first.QR.URL, secret)
		}
	}

	// --- The PNG sticker: "UNIT A1" above the QR, property/rent/short code
	// below it — a taller-than-wide composition now, not a bare square QR.
	w := call(http.MethodGet, qrPath+".png", "", cookie)
	if w.Code != http.StatusOK || w.Header().Get("Content-Type") != "image/png" || !strings.HasPrefix(w.Header().Get("Content-Disposition"), "inline") {
		t.Fatalf("png: %d %s %s", w.Code, w.Header().Get("Content-Type"), w.Header().Get("Content-Disposition"))
	}
	img, err := png.Decode(bytes.NewReader(w.Body.Bytes()))
	if err != nil {
		t.Fatalf("the PNG does not decode: %v", err)
	}
	if b := img.Bounds(); b.Dx() < 400 || b.Dy() < b.Dx() {
		t.Errorf("sticker is %dx%d, want at least 400 wide and taller than wide (title + QR + text)", b.Dx(), b.Dy())
	}
	wantFilename := slug + "-unit-A1-qr.png"
	if w := call(http.MethodGet, qrPath+".png?download=1", "", cookie); !strings.HasPrefix(w.Header().Get("Content-Disposition"), "attachment") ||
		!strings.Contains(w.Header().Get("Content-Disposition"), wantFilename) {
		t.Errorf("download disposition = %q, want it to contain %q", w.Header().Get("Content-Disposition"), wantFilename)
	}

	// --- A scan counts and redirects to the ordinary pay page (SMS still
	// applies). Relative, not built from frontendURL: a phone scanning the
	// sticker can't reach the frontend dev server's own address, and
	// frontendURL is for email links only.
	payURL := "/pay/" + slug + "/A1"
	scan := call(http.MethodGet, "/q/"+first.QR.Token, "", "")
	if scan.Code != http.StatusFound || location(scan) != payURL || scan.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("scan: %d -> %q cache %q, want 302 -> %s", scan.Code, location(scan), scan.Header().Get("Cache-Control"), payURL)
	}
	// Typed by hand: lower case, with dashes.
	if w := call(http.MethodGet, "/q/"+strings.ToLower(first.QR.ShortCode), "", ""); location(w) != payURL {
		t.Errorf("hand-typed code -> %q, want %s", location(w), payURL)
	}
	if got := getQR(); got.QR.ScanCount != 2 || got.QR.LastScannedAt == "" {
		t.Errorf("after two scans: count %d, last %q", got.QR.ScanCount, got.QR.LastScannedAt)
	}

	// --- Unknown and malformed codes go to the same inactive page.
	inactive := "/pay/inactive"
	for _, tok := range []string{"K7QM2XH9PTRB", "nonsense", "K7QM-2XH9-PTRB", "0000"} {
		if w := call(http.MethodGet, "/q/"+tok, "", ""); w.Code != http.StatusFound || location(w) != inactive {
			t.Errorf("scan %q: %d -> %q, want 302 -> %s", tok, w.Code, location(w), inactive)
		}
	}

	// --- Another firm cannot see or rotate this lease's code.
	if w := call(http.MethodGet, qrPath, "", otherCookie); w.Code != http.StatusNotFound {
		t.Errorf("another firm's GET qr: %d, want 404", w.Code)
	}
	if w := call(http.MethodGet, qrPath+".png", "", otherCookie); w.Code != http.StatusNotFound {
		t.Errorf("another firm's png: %d, want 404", w.Code)
	}
	if w := call(http.MethodPost, qrPath+"/rotate", "", otherCookie); w.Code != http.StatusNotFound {
		t.Errorf("another firm's rotate: %d, want 404", w.Code)
	}

	// --- Rotate: the old sticker dies at once, the new one works.
	w = call(http.MethodPost, qrPath+"/rotate", "", cookie)
	if w.Code != http.StatusCreated {
		t.Fatalf("rotate: %d %s", w.Code, w.Body)
	}
	var rotated qrBody
	_ = json.Unmarshal(w.Body.Bytes(), &rotated)
	if rotated.QR.Token == "" || rotated.QR.Token == first.QR.Token || rotated.QR.ScanCount != 0 {
		t.Fatalf("rotated = %+v", rotated.QR)
	}
	if w := call(http.MethodGet, "/q/"+first.QR.Token, "", ""); location(w) != inactive {
		t.Errorf("the rotated-out sticker -> %q, want %s", location(w), inactive)
	}
	if w := call(http.MethodGet, "/q/"+rotated.QR.Token, "", ""); location(w) != payURL {
		t.Errorf("the new sticker -> %q, want %s", location(w), payURL)
	}
	// A revoked code and one that never existed are indistinguishable.
	revoked := call(http.MethodGet, "/q/"+first.QR.Token, "", "")
	unknown := call(http.MethodGet, "/q/K7QM2XH9PTRB", "", "")
	if revoked.Code != unknown.Code || location(revoked) != location(unknown) || revoked.Body.String() != unknown.Body.String() {
		t.Error("a revoked token answers differently from an unknown one")
	}

	// --- Ending the lease revokes the sticker and blocks new codes.
	if w := call(http.MethodPatch, "/v1/leases/"+lease.ID.String(), `{"status":"terminated"}`, cookie); w.Code != http.StatusOK {
		t.Fatalf("terminate lease: %d %s", w.Code, w.Body)
	}
	if w := call(http.MethodGet, "/q/"+rotated.QR.Token, "", ""); location(w) != inactive {
		t.Errorf("a terminated lease's sticker -> %q, want %s", location(w), inactive)
	}
	for _, r := range []struct{ method, path string }{{http.MethodGet, qrPath}, {http.MethodGet, qrPath + ".png"}, {http.MethodPost, qrPath + "/rotate"}} {
		if w := call(r.method, r.path, "", cookie); w.Code != http.StatusConflict {
			t.Errorf("%s %s on a terminated lease: %d, want 409", r.method, r.path, w.Code)
		}
	}
}
