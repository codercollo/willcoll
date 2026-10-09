package main

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/alexedwards/scs/v2/memstore"
	"github.com/codercollo/willcoll/backend/internal/platform/data"
	"github.com/codercollo/willcoll/backend/internal/shared/auth"
	"github.com/codercollo/willcoll/backend/internal/shared/auth/routematrix"
	"github.com/codercollo/willcoll/backend/internal/shared/payhero"
	"github.com/codercollo/willcoll/backend/internal/testenv"
	"github.com/codercollo/willcoll/backend/pkg/moneyfmt"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// The golden route x role matrix for platform-api (docs/security/c4-spec.md
// C4.2): every registered route, called as every principal, through the real
// router and a real willcoll_platform, compared with
// internal/shared/auth/testdata/route_matrix.golden. Every call that can
// change data gets a brand-new fixture.

const matrixServiceToken = "matrix-service-credential"

// stepUpGatedRoutes are the platform routes that demand a recent password
// re-entry before the handler runs: the Super Admin's consequential actions
// (c4-matrix-fixes.md item 6).
var stepUpGatedRoutes = map[string]bool{
	"POST /v1/admin/managers/:id/suspend":           true,
	"POST /v1/admin/managers/:id/reinstate":         true,
	"POST /v1/admin/unclaimed-receipts/:id/resolve": true,
	"POST /v1/admin/plans":                          true,
	"PATCH /v1/admin/plans/:id":                     true,
	"POST /v1/admin/plans/:id/archive":              true,
	"POST /v1/admin/plans/:id/restore":              true,
}

// stepUpInHandlerRoutes demand step-up inside the handler, after the permission
// and owner-role checks, and only for some requests: an invite always, a staff
// PATCH only when authority changes. The plain matrix row (an empty body) never
// meets it, so these routes are shown by their variant rows.
var stepUpInHandlerRoutes = map[string]bool{
	"POST /v1/staff":      true,
	"PATCH /v1/staff/:id": true,
}

// routesWithFieldChecks are the platform routes whose rule depends on WHAT is
// being changed, not only on who asks (a role, the owner role, a module or
// property scope, a status): an empty body proves nothing about them. Each lists
// the variant rows that must exist in the golden matrix, one per protected field.
// TestEveryFieldCheckedRouteHasItsVariantRows holds the list to the golden file,
// and TestEveryFieldCheckInTheCodeIsListed holds it to the handlers' source.
var routesWithFieldChecks = map[string][]string{
	"PATCH /v1/staff/:id": {"role change", "module_access change", "property_access change", "deactivate", "name only", "grant owner"},
	"POST /v1/staff":      {"invite viewer", "invite owner"},
}

type matrixRig struct {
	app    *application
	router *gin.Engine
	app_   *sql.DB // willcoll_platform_app
	admin  *sql.DB // willcoll_platform_admin
	models data.Models
	sm     *scs.SessionManager

	adminID uuid.UUID

	// The current fixture.
	firm       uuid.UUID
	staffIDs   map[string]uuid.UUID // role key -> staff id
	targetID   uuid.UUID            // a staff member the PATCH /staff/:id routes act on
	planID     uuid.UUID
	receiptID  uuid.UUID
	otherFirm  uuid.UUID
	cookieName string
}

func newMatrixRig(t *testing.T) *matrixRig {
	t.Helper()
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
	r := &matrixRig{app_: open(dsns[0]), admin: open(dsns[1]), cookieName: "willcoll_session"}
	r.models = data.NewModels(r.app_, r.admin, 5*time.Second)
	r.models.Billing.Env = "development"

	sm := scs.New()
	sm.Store = memstore.New()
	sm.Cookie.Name = r.cookieName
	r.sm = sm

	r.app = &application{
		logger:         slog.New(slog.NewTextHandler(io.Discard, nil)),
		models:         r.models,
		db:             r.app_,
		adminDB:        r.admin,
		sessionManager: sm,
		mailer:         &captureMailer{},
		payhero:        payhero.NewClient("", "", nil), // unconfigured: every call fails cleanly
		markHandlers:   true,                           // the matrix tells "refused" from "the handler ran"
	}
	r.app.config.service.Current = matrixServiceToken
	r.app.config.cors.trustedOrigins = []string{"http://localhost:3000"}
	router, err := r.app.routes()
	if err != nil {
		t.Fatal(err)
	}
	r.router = router

	// The one Super Admin (the table holds a single row, so reuse any that exists).
	err = r.app_.QueryRowContext(context.Background(), `SELECT id FROM admins LIMIT 1`).Scan(&r.adminID)
	if errors.Is(err, sql.ErrNoRows) {
		r.adminID = uuid.New()
		_, err = r.app_.Exec(`INSERT INTO admins (id, name, email, password_hash) VALUES ($1, 'Root', $2, '\x01'::bytea)`,
			r.adminID, "root-"+r.adminID.String()[:8]+"@matrix.example.com")
	}
	if err != nil {
		t.Fatal(err)
	}
	r.reseed(t)
	return r
}

func (r *matrixRig) tenantTx(t *testing.T, tenant uuid.UUID, fn func(tx *sql.Tx)) {
	t.Helper()
	tx, err := r.app_.Begin()
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

func (r *matrixRig) newFirm(t *testing.T) uuid.UUID {
	t.Helper()
	id := uuid.New()
	_, err := r.app_.Exec(`INSERT INTO managers (id, firm_name, username, email, phone, password_hash, status, activated_at)
		VALUES ($1, 'Matrix Firm', $2, $3, '+254700000000', '\x01'::bytea, 'active', now())`,
		id, "m"+id.String()[:12], id.String()[:8]+"@matrix.example.com")
	if err != nil {
		t.Fatal(err)
	}
	r.tenantTx(t, id, func(tx *sql.Tx) {
		if _, err := tx.Exec(`INSERT INTO manager_modules (manager_id, product, status, activated_at) VALUES ($1, 'rentals', 'active', now())`, id); err != nil {
			t.Fatal(err)
		}
	})
	return id
}

func (r *matrixRig) newStaff(t *testing.T, firm uuid.UUID, role, access string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	r.tenantTx(t, firm, func(tx *sql.Tx) {
		if _, err := tx.Exec(`INSERT INTO staff (id, manager_id, name, email, password_hash, role, module_access, property_access, status)
			VALUES ($1, $2, 'Staffer', $3, '\x01'::bytea, $4, '{rentals}', $5::jsonb, 'active')`,
			id, firm, id.String()[:8]+"@matrix.example.com", role, access); err != nil {
			t.Fatal(err)
		}
	})
	return id
}

// reseed seeds a brand-new fixture: a firm with a staff member of every role,
// a staff member to act on, another firm, a plan and an open unclaimed receipt.
func (r *matrixRig) reseed(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	r.firm, r.otherFirm = r.newFirm(t), r.newFirm(t)
	r.staffIDs = map[string]uuid.UUID{}
	for key, role := range map[string]string{"viewer": data.RoleViewer, "officer": data.RoleOfficer, "accountant": data.RoleAccountant, "staff_owner": data.RoleOwner} {
		r.staffIDs[key] = r.newStaff(t, r.firm, role, `{}`)
	}
	// Property A only: the platform holds no properties, so this is scoped in name only.
	r.staffIDs["officer_scoped"] = r.newStaff(t, r.firm, data.RoleOfficer, `{"rentals":["`+uuid.NewString()+`"]}`)
	// The member the PATCH /staff/:id rows act on: an officer, so "officer to
	// accountant" is the realistic promotion.
	r.targetID = r.newStaff(t, r.firm, data.RoleOfficer, `{}`)

	// A valid plan (a positive price), so editing it can succeed.
	plan, err := r.models.Plans.Create(ctx, data.PlanInput{
		Product: data.ProductRentals, Name: "Matrix " + uuid.NewString()[:8], Price: moneyfmt.FromCents(100000), BillingInterval: "monthly",
		IncludedSeats: 1, Features: []string{}, PricingType: data.PricingFlat,
	}, true)
	if err != nil {
		t.Fatal(err)
	}
	r.planID = plan.ID

	receipt := "MATRIX" + strings.ToUpper(uuid.NewString()[:8])
	if _, err := r.models.Unclaimed.Record(ctx, receipt, "CH-"+receipt, data.ProductRentals, moneyfmt.Money{}, "254700000000", nil); err != nil {
		t.Fatal(err)
	}
	open, err := r.models.Unclaimed.ListOpen(ctx, 1000)
	if err != nil {
		t.Fatal(err)
	}
	r.receiptID = uuid.Nil
	for _, o := range open {
		if o.MpesaReceipt == receipt {
			r.receiptID = o.ID
		}
	}
	if r.receiptID == uuid.Nil {
		t.Fatal("the seeded unclaimed receipt was not found")
	}
}

type matrixCreds struct {
	cookie *http.Cookie
	bearer string
}

func (r *matrixRig) session(t *testing.T, principalType string, id uuid.UUID) *http.Cookie {
	t.Helper()
	ctx, err := r.sm.Load(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	r.sm.Put(ctx, sessionKeyPrincipalType, principalType)
	r.sm.Put(ctx, sessionKeyPrincipalID, id.String())
	token, _, err := r.sm.Commit(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return &http.Cookie{Name: r.cookieName, Value: token}
}

func (r *matrixRig) principals(t *testing.T) map[string]matrixCreds {
	t.Helper()
	staff := func(key string) matrixCreds {
		return matrixCreds{cookie: r.session(t, principalStaff, r.staffIDs[key])}
	}
	return map[string]matrixCreds{
		"anonymous": {},
		// platform-api has no pay surface: a bearer token it knows nothing about.
		"pay_token":      {bearer: "pay.unknown-to-platform"},
		"viewer":         staff("viewer"),
		"officer":        staff("officer"),
		"officer_scoped": staff("officer_scoped"),
		"accountant":     staff("accountant"),
		"staff_owner":    staff("staff_owner"),
		"firm_login":     {cookie: r.session(t, principalManager, r.firm)},
		"super_admin":    {cookie: r.session(t, principalAdmin, r.adminID)},
		"service":        {bearer: matrixServiceToken},
	}
}

func (r *matrixRig) path(route string) string {
	p := strings.NewReplacer(
		"/managers/:id", "/managers/"+r.otherFirm.String(),
		"/plans/:id", "/plans/"+r.planID.String(),
		"/unclaimed-receipts/:id", "/unclaimed-receipts/"+r.receiptID.String(),
		"/staff/:id", "/staff/"+r.targetID.String(),
		"/invoices/:id", "/invoices/"+uuid.NewString(),
		"/modules/:product", "/modules/estates",
		"/billing/:product", "/billing/rentals",
	).Replace(route)
	return p
}

// call sends one request and reports the status, the body, and whether the
// route's own handler ran (the test-only marker, auth.MarkReached).
func (r *matrixRig) call(method, target, payload string, c matrixCreds) (code int, body string, reached bool) {
	var reader *strings.Reader
	if payload == "" && method != http.MethodGet && method != http.MethodDelete {
		payload = `{}`
	}
	reader = strings.NewReader(payload)
	req := httptest.NewRequest(method, target, reader)
	req.Header.Set("Content-Type", "application/json")
	if c.cookie != nil {
		req.AddCookie(c.cookie)
	}
	if c.bearer != "" {
		req.Header.Set("Authorization", "Bearer "+c.bearer)
	}
	w := httptest.NewRecorder()
	r.router.ServeHTTP(w, req)
	return w.Code, w.Body.String(), w.Header().Get(auth.ReachedHeader) != ""
}

// openStepUp opens a step-up window on cookie's session, the state a successful
// POST /v1/sessions/step-up leaves behind.
func (r *matrixRig) openStepUp(t *testing.T, cookie *http.Cookie) {
	t.Helper()
	if cookie == nil {
		return
	}
	ctx, err := r.sm.Load(context.Background(), cookie.Value)
	if err != nil {
		t.Fatal(err)
	}
	r.sm.Put(ctx, auth.SessionKeyStepUpUntil, auth.StepUpUntil(time.Now()))
	if _, _, err := r.sm.Commit(ctx); err != nil {
		t.Fatal(err)
	}
}

// matrixVariant is one realistic request to a route whose rule depends on what is
// being changed (c4-matrix-fixes.md item 1). body is evaluated after the fixture
// is seeded, so it can name the fixture's ids.
type matrixVariant struct {
	route string // "METHOD /registered/path"
	name  string
	body  func(r *matrixRig) string
}

func (v matrixVariant) key() string { return routematrix.VariantKey(v.route, v.name) }

func matrixInviteBody(role string) func(r *matrixRig) string {
	return func(*matrixRig) string {
		return `{"name":"New Clerk","email":"clerk-` + uuid.NewString()[:8] + `@matrix.example.com","role":"` + role + `","module_access":["rentals"]}`
	}
}

func fixedBody(s string) func(*matrixRig) string { return func(*matrixRig) string { return s } }

// matrixVariants are the variant rows of the platform matrix.
func matrixVariants() []matrixVariant {
	patch := "PATCH /v1/staff/:id"
	return []matrixVariant{
		// The member acted on is an officer. Authority changes need step-up; a
		// rename does not; granting the owner role is the firm login's alone (D3).
		{patch, "role change", fixedBody(`{"role":"accountant"}`)},
		{patch, "module_access change", fixedBody(`{"module_access":[]}`)},
		{patch, "property_access change", func(*matrixRig) string {
			return `{"property_access":{"rentals":["` + uuid.NewString() + `"]}}`
		}},
		{patch, "deactivate", fixedBody(`{"status":"disabled"}`)},
		{patch, "name only", fixedBody(`{"name":"Renamed Clerk"}`)},
		{patch, "grant owner", fixedBody(`{"role":"owner"}`)},
		{"POST /v1/staff", "invite viewer", matrixInviteBody(data.RoleViewer)},
		{"POST /v1/staff", "invite owner", matrixInviteBody(data.RoleOwner)},

		// The Super Admin's actions with a body that can succeed.
		{"POST /v1/admin/plans", "create plan", func(*matrixRig) string {
			return `{"product":"rentals","name":"Matrix ` + uuid.NewString()[:8] + `","price":"1000","billing_interval":"monthly"}`
		}},
		{"PATCH /v1/admin/plans/:id", "rename", fixedBody(`{"name":"Renamed plan"}`)},
		{"POST /v1/admin/unclaimed-receipts/:id/resolve", "with note", fixedBody(`{"note":"refunded to the payer"}`)},
		{"POST /v1/admin/managers/:id/suspend", "with reason", fixedBody(`{"reason":"chargeback dispute"}`)},
	}
}

// runRow calls one row (a route, or a variant of it) as every principal and
// returns the row's cells. A call that can change data gets a brand-new fixture
// for each principal. A refusal that asks for step-up is repeated on yet another
// fresh fixture with an open step-up window, and the cell records both.
func (r *matrixRig) runRow(t *testing.T, method, route string, body func(*matrixRig) string, creds *map[string]matrixCreds) map[string]string {
	row := map[string]string{}
	for _, p := range routematrix.Principals {
		once := func(stepUp bool) string {
			if method != http.MethodGet {
				r.reseed(t)
				*creds = r.principals(t)
			}
			if stepUp {
				r.openStepUp(t, (*creds)[p].cookie)
			}
			payload := ""
			if body != nil {
				payload = body(r)
			}
			code, resp, reached := r.call(method, r.path(route), payload, (*creds)[p])
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

func TestRouteMatrixPlatform(t *testing.T) {
	r := newMatrixRig(t)
	routes := r.router.Routes()
	sort.Slice(routes, func(i, j int) bool {
		ri, rj := routes[i].Method == http.MethodGet, routes[j].Method == http.MethodGet
		if ri != rj {
			return ri
		}
		return auth.Key(routes[i].Method, routes[i].Path) < auth.Key(routes[j].Method, routes[j].Path)
	})

	got := routematrix.Matrix{}
	creds := r.principals(t)
	for _, rt := range routes {
		route := auth.Key(rt.Method, rt.Path)
		got[route] = r.runRow(t, rt.Method, rt.Path, nil, &creds)
	}
	for _, v := range matrixVariants() {
		method, path, _ := strings.Cut(v.route, " ")
		got[v.key()] = r.runRow(t, method, path, v.body, &creds)
	}

	routematrix.CheckInvariants(t, r.app.routeGuards, got, routematrix.Rules{
		StepUp:          stepUpGatedRoutes,
		StepUpInHandler: stepUpInHandlerRoutes,
		FirmRoute: func(path string) bool {
			return !strings.HasPrefix(path, "/v1/admin/") && !strings.HasPrefix(path, "/internal/")
		},
		Internal: func(path string) bool { return strings.HasPrefix(path, "/internal/v1/") },
	})

	routematrix.Check(t, routematrix.APIPlatform, got)
}
