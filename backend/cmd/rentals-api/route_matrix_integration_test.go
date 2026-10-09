package main

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	platformdata "github.com/codercollo/willcoll/backend/internal/platform/data"
	"github.com/codercollo/willcoll/backend/internal/rentals/data"
	"github.com/codercollo/willcoll/backend/internal/shared/auth"
	"github.com/codercollo/willcoll/backend/internal/shared/auth/routematrix"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// The golden route x role matrix for rentals-api (docs/security/c4-spec.md
// C4.2, docs/security/c4-matrix-fixes.md): every registered route, called as
// every principal, through the real router and the real database, compared with
// internal/shared/auth/testdata/route_matrix.golden.
//
// Every call that can change data gets a brand-new fixture, so what one
// principal does never changes what the next one sees. All callers ask for
// property B's resources (the scoped officer holds property A only), except the
// pay routes, which are asked for unit A1, the one the pay token belongs to.
//
// A route whose rule depends on WHAT is changed (a payment channel, a rent, a
// water rate, a cash payment) is also called with realistic bodies, as named
// variant rows: an empty body proves nothing about those.

// stepUpInHandlerRoutes demand step-up inside the handler, only when a request
// changes the protected field (a payment channel), after the permission check.
var stepUpInHandlerRoutes = map[string]bool{
	"POST /v1/properties":      true,
	"PATCH /v1/properties/:id": true,
	"POST /v1/landlords":       true,
	"PATCH /v1/landlords/:id":  true,
}

// routesWithFieldChecks are the Rentals routes whose rule depends on WHAT is
// being changed, not only on who asks: a payment channel, a rent or other amount,
// a water rate, a hand-entered payment. Each lists the variant rows that must
// exist in the golden matrix, one per protected field.
// TestEveryFieldCheckedRouteHasItsVariantRows holds the list to the golden file,
// and TestEveryFieldCheckInTheCodeIsListed holds it to the handlers' source.
var routesWithFieldChecks = map[string][]string{
	"POST /v1/properties":              {"with channel", "no channel"},
	"PATCH /v1/properties/:id":         {"channel change", "channel echo", "name only"},
	"PATCH /v1/units/:id":              {"rent change", "rent echo", "label only"},
	"POST /v1/properties/:id/units":    {"with rent", "no money fields"},
	"PUT /v1/properties/:id/water":     {"rate change", "rate echo", "reading only"},
	"POST /v1/units/:id/rent/payments": {"cash payment"},
	"POST /v1/payments/:id/confirm":    {"confirm pending"},
	"POST /v1/landlords":               {"with payout", "new landlord"},
	"PATCH /v1/landlords/:id":          {"payout change", "payout echo", "name only"},
}

// matrixCreds are one principal's credentials for one fixture.
type matrixCreds struct{ cookie, bearer string }

// matrixReseed seeds a brand-new fixture and the extras only the matrix needs: a
// co-payer on lease B, a pending cash payment on unit B1 (recorded by someone
// else), and a payment channel on property B.
func (s *scopeHTTP) matrixReseed(t *testing.T) {
	t.Helper()
	s.reseed(t)
	ctx := t.Context()
	s.payerB = &data.LeasePayer{Name: "CO PAYER", Phone: strPtr("+254722222222")}
	if err := s.models.Leases.AddPayer(ctx, s.firm, s.leaseB.ID, s.payerB); err != nil {
		t.Fatal(err)
	}
	// Recorded by a member who is not the confirmer in any column of the matrix.
	pending, err := s.models.Rent.PostPayment(ctx, s.firm, s.firm, s.unitB.ID, data.PaymentInput{
		Amount: money("700"), Source: "manual", Reference: "PEND-" + uuid.NewString()[:8],
		Pending: true, RecordedBy: uuid.New(), RecordedByRole: "officer",
	})
	if err != nil {
		t.Fatal(err)
	}
	s.pendingB = pending.PaymentID
	s.channelB = "CH-" + strings.ToUpper(uuid.NewString()[:8])
	tenantSQL(t, s.conn, s.firm, func(tx *sql.Tx) {
		if _, err := tx.Exec(`UPDATE properties SET payhero_channel_id = $1 WHERE id = $2`, s.channelB, s.propB.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(`UPDATE landlords SET bank_account_number = $1 WHERE id = $2`, matrixPayoutAccount, s.landlordB); err != nil {
			t.Fatal(err)
		}
	})
}

// matrixPayoutAccount is the bank account number landlord B holds in every fixture.
const matrixPayoutAccount = "0123456789"

func strPtr(s string) *string { return &s }

// matrixPrincipals signs every principal in against the current fixture.
func (s *scopeHTTP) matrixPrincipals(t *testing.T) map[string]matrixCreds {
	t.Helper()
	staff := func(role string, scope map[string][]uuid.UUID) matrixCreds {
		_, cookie := s.stub.newStaff(t, s.firm, role, scope)
		return matrixCreds{cookie: cookie}
	}
	payToken, err := s.app.signPaySession(paySession{TenantID: s.firm, UnitID: s.unitA.ID, LeaseID: s.leaseA.ID, Phone: s.leaseA.PrimaryPhone},
		time.Now().Add(15*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	return map[string]matrixCreds{
		"anonymous":      {},
		"pay_token":      {bearer: payToken},
		"viewer":         staff(platformdata.RoleViewer, nil),
		"officer":        staff(platformdata.RoleOfficer, nil),
		"officer_scoped": staff(platformdata.RoleOfficer, map[string][]uuid.UUID{"rentals": {s.propA.ID}}),
		"accountant":     staff(platformdata.RoleAccountant, nil),
		"staff_owner":    staff(platformdata.RoleOwner, nil),
		"firm_login":     {cookie: s.stub.loginManager(t, s.firm)},
		"super_admin":    {cookie: s.stub.loginAdmin(t)},
		"service":        {bearer: "service-credential-" + uuid.NewString()},
	}
}

// matrixPath fills a route's path parameters from the current fixture. onPending
// points the route's :id at the pending cash payment instead of the posted one.
func (s *scopeHTTP) matrixPath(route string, onPending bool) string {
	id := uuid.NewString()
	if kind, ok := scopeKindForRoute(route); ok {
		id = map[string]uuid.UUID{
			"property": s.propB.ID, "unit": s.unitB.ID, "lease": s.leaseB.ID,
			"landlord": s.landlordB, "payment": s.paymentB, "ledger_entry": s.entryB,
		}[string(kind)].String()
	}
	if onPending {
		id = s.pendingB.String()
	}
	period := "2026-08"
	if strings.HasSuffix(route, "/invoices/:period") {
		period += ".pdf"
	}
	payer := uuid.NewString()
	if s.payerB != nil {
		payer = s.payerB.ID.String()
	}
	p := strings.NewReplacer(":id", id, ":payerId", payer, ":type", "RENT", ":period", period,
		":propertySlug", s.propA.Slug, ":unitCode", "A1", ":token", "matrix-token").Replace(route)
	if strings.HasSuffix(p, "/rent/overview") || strings.HasSuffix(p, "/garbage/generate") {
		p += "?period=2026-09"
	}
	return p
}

// matrixCall sends one request and reports the status, the body, and whether the
// route's own handler ran (the test-only marker, auth.MarkReached).
func (s *scopeHTTP) matrixCall(method, target, payload string, c matrixCreds) (code int, body string, reached bool) {
	if payload == "" && method != http.MethodGet && method != http.MethodDelete {
		payload = `{}`
	}
	req := httptest.NewRequest(method, target, strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	if c.cookie != "" {
		req.Header.Set("Cookie", c.cookie)
	}
	if c.bearer != "" {
		req.Header.Set("Authorization", "Bearer "+c.bearer)
	}
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)
	return w.Code, w.Body.String(), w.Header().Get(auth.ReachedHeader) != ""
}

// matrixVariant is one realistic request to a route whose rule depends on what is
// being changed (c4-matrix-fixes.md item 1). body is evaluated after the fixture
// is seeded, so it can name the fixture's ids; query is appended to the path.
type matrixVariant struct {
	route string // "METHOD /registered/path"
	name  string
	query string
	body  func(s *scopeHTTP) string
}

func (v matrixVariant) key() string { return routematrix.VariantKey(v.route, v.name) }

// variantsOnPendingPayment point their route's :id at the pending cash payment,
// the one a confirmation or rejection acts on (the plain rows act on a posted
// payment, which has nothing to confirm).
var variantsOnPendingPayment = map[string]bool{
	routematrix.VariantKey("POST /v1/payments/:id/confirm", "confirm pending"): true,
	routematrix.VariantKey("POST /v1/payments/:id/reject", "with reason"):      true,
}

func fixedBody(b string) func(*scopeHTTP) string { return func(*scopeHTTP) string { return b } }

// matrixVariants are the variant rows of the Rentals matrix.
func matrixVariants() []matrixVariant {
	newUnit := func(extra string) func(*scopeHTTP) string {
		return func(*scopeHTTP) string {
			return `{"unit_code":"N` + uuid.NewString()[:5] + `"` + extra + `}`
		}
	}
	newProperty := func(channel string) func(*scopeHTTP) string {
		return func(s *scopeHTTP) string {
			return `{"landlord_id":"` + s.landlordB.String() + `","name":"New Court","location":"Nairobi","slug":"nc-` + uuid.NewString()[:8] + `"` + channel + `}`
		}
	}
	water := func(rate string) func(*scopeHTTP) string {
		return func(s *scopeHTTP) string {
			return `{"readings":[{"unit_id":"` + s.unitB.ID.String() + `","current_reading":"20"` + rate + `}]}`
		}
	}
	return []matrixVariant{
		// 1b. A payment channel decides where tenants' money lands: owner only,
		// with step-up, wherever it can be set. Re-sending the current value, or
		// editing anything else, is an ordinary property edit.
		{"PATCH /v1/properties/:id", "channel change", "", func(*scopeHTTP) string { return `{"payhero_channel_id":"CH-NEW-` + uuid.NewString()[:6] + `"}` }},
		{"PATCH /v1/properties/:id", "channel echo", "", func(s *scopeHTTP) string { return `{"payhero_channel_id":"` + s.channelB + `"}` }},
		{"PATCH /v1/properties/:id", "name only", "", fixedBody(`{"name":"Bravo Court"}`)},
		{"POST /v1/properties", "with channel", "", newProperty(`,"payhero_channel_id":"CH-NEW-` + uuid.NewString()[:6] + `"`)},
		{"POST /v1/properties", "no channel", "", newProperty("")},

		// 4. D1: an officer edits labels and takes readings, but cannot change
		// what a tenant is charged. One shared guard (moneyguard.go).
		{"PATCH /v1/units/:id", "rent change", "", fixedBody(`{"rent_amount":"1"}`)},
		{"PATCH /v1/units/:id", "rent echo", "", fixedBody(`{"unit_code":"B1","rent_amount":"10000"}`)},
		{"PATCH /v1/units/:id", "label only", "", fixedBody(`{"meter_number":"M-77"}`)},
		{"POST /v1/properties/:id/units", "with rent", "", newUnit(`,"rent_amount":"8000"`)},
		{"POST /v1/properties/:id/units", "no money fields", "", newUnit("")},
		{"PUT /v1/properties/:id/water", "rate change", "?period=2026-09", water(`,"rate":"1"`)},
		{"PUT /v1/properties/:id/water", "rate echo", "?period=2026-09", water(`,"rate":"150"`)},
		{"PUT /v1/properties/:id/water", "reading only", "?period=2026-09", water("")},

		// 3. A hand-entered payment by someone who cannot confirm it waits as
		// pending and posts nothing; a decision on it comes from someone else.
		{"POST /v1/units/:id/rent/payments", "cash payment", "", func(*scopeHTTP) string {
			return `{"amount":"500","source":"manual","reference":"CASH-` + uuid.NewString()[:8] + `","ledger_type":"rent"}`
		}},
		{"POST /v1/payments/:id/confirm", "confirm pending", "", nil},

		// Plain realistic bodies (item 2): so that at least one allowed principal
		// gets a 2xx where the empty body only reached a validation error.
		{"POST /v1/ledger-entries/:id/reverse", "with reason", "", fixedBody(`{"reason":"posted in error"}`)},
		{"POST /v1/units/:id/rent/charges", "late fee", "", fixedBody(`{"amount":"500","description":"late fee","ledger_type":"rent"}`)},
		{"POST /v1/leases/:id/payers", "add payer", "", fixedBody(`{"name":"Second Payer","phone":"+254733333333"}`)},
		{"POST /v1/landlords", "new landlord", "", fixedBody(`{"name":"New Landlord","phone":"+254744444444"}`)},

		// A landlord's payout destination decides where the landlord's money is
		// sent: owner only, with step-up, like a payment channel. Re-sending the
		// current value, or editing anything else, is an ordinary landlord edit.
		{"POST /v1/landlords", "with payout", "", fixedBody(`{"name":"Paid Landlord","phone":"+254755555555","bank_name":"Equity","bank_account_number":"9988776655"}`)},
		{"PATCH /v1/landlords/:id", "payout change", "", fixedBody(`{"bank_account_number":"1111222233"}`)},
		{"PATCH /v1/landlords/:id", "payout echo", "", fixedBody(`{"bank_account_number":"` + matrixPayoutAccount + `"}`)},
		{"PATCH /v1/landlords/:id", "name only", "", fixedBody(`{"name":"Renamed Landlord"}`)},
		{"PUT /v1/properties/:id/rent-schedule", "set rent", "", func(s *scopeHTTP) string {
			return `[{"unit_id":"` + s.unitB.ID.String() + `","rent_amount":"11000"}]`
		}},
		{"POST /v1/payments/:id/reject", "with reason", "", fixedBody(`{"reason":"no cash was handed over"}`)},
	}
}

// runRow calls one row (a route, or a variant of it) as every principal and
// returns the row's cells. A call that can change data gets a brand-new fixture
// for each principal. A refusal that asks for step-up is repeated on yet another
// fresh fixture with an open step-up window, and the cell records both.
func (s *scopeHTTP) runRow(t *testing.T, method, route, query string, onPending bool, body func(*scopeHTTP) string, creds *map[string]matrixCreds) map[string]string {
	row := map[string]string{}
	for _, p := range routematrix.Principals {
		once := func(stepUp bool) string {
			if method != http.MethodGet {
				s.matrixReseed(t)
				*creds = s.matrixPrincipals(t)
			}
			if stepUp {
				s.stub.openStepUp(t, (*creds)[p].cookie)
			}
			payload := ""
			if body != nil {
				payload = body(s)
			}
			code, resp, reached := s.matrixCall(method, s.matrixPath(route, onPending)+query, payload, (*creds)[p])
			return routematrix.Cell(code, resp, reached)
		}
		cell := once(false)
		if routematrix.AskedForStepUp(cell) {
			cell = routematrix.WithStepUp(cell, once(true))
		}
		row[p] = cell
	}
	return row
}

func TestRouteMatrixRentals(t *testing.T) {
	s := newScopeHTTPMarked(t, true)
	// The Super Admin's health and backup pages scan catalog statistics, which on
	// a cold scratch database can outlast the per-query timeout and turn into a
	// 500/503. The matrix records the access rule, not that timing.
	s.app.models.Platform.HealthTimeout = 2 * time.Minute
	routes := s.router.(*gin.Engine).Routes()

	// Reads first, on the original fixture (they change nothing); then every
	// call that may change data, each on a fixture of its own.
	sort.Slice(routes, func(i, j int) bool {
		ri, rj := routes[i].Method == http.MethodGet, routes[j].Method == http.MethodGet
		if ri != rj {
			return ri
		}
		return auth.Key(routes[i].Method, routes[i].Path) < auth.Key(routes[j].Method, routes[j].Path)
	})

	got := routematrix.Matrix{}
	s.matrixReseed(t)
	creds := s.matrixPrincipals(t)
	for _, r := range routes {
		got[auth.Key(r.Method, r.Path)] = s.runRow(t, r.Method, r.Path, "", false, nil, &creds)
	}
	for _, v := range matrixVariants() {
		method, path, _ := strings.Cut(v.route, " ")
		got[v.key()] = s.runRow(t, method, path, v.query, variantsOnPendingPayment[v.key()], v.body, &creds)
	}

	routematrix.CheckInvariants(t, s.app.routeGuards, got, routematrix.Rules{
		StepUp:          stepUpGatedRoutes,
		StepUpInHandler: stepUpInHandlerRoutes,
		ScopedResource:  func(path string) bool { _, ok := scopeKindForRoute(path); return ok },
		FirmRoute:       func(path string) bool { return !strings.HasPrefix(path, "/v1/admin/") },
	})

	// The pay token is for unit A1 only: the same routes for unit B1 are not its to use.
	s.matrixReseed(t)
	creds = s.matrixPrincipals(t)
	for _, r := range routes {
		if !strings.HasPrefix(r.Path, "/v1/pay/:propertySlug/") {
			continue
		}
		target := strings.NewReplacer(":propertySlug", s.propB.Slug, ":unitCode", "B1").Replace(r.Path)
		if code, body, _ := s.matrixCall(r.Method, target, "", creds["pay_token"]); code/100 == 2 && !strings.HasSuffix(r.Path, ":unitCode") {
			t.Errorf("A1's pay token got %d from %s %s for unit B1: %s", code, r.Method, target, body)
		}
	}

	routematrix.Check(t, routematrix.APIRentals, got)
}
