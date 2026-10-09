package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/alexedwards/scs/v2/memstore"
	"github.com/codercollo/willcoll/backend/internal/platform/data"
	"github.com/codercollo/willcoll/backend/internal/platform/db/mock"
	"github.com/codercollo/willcoll/backend/internal/platform/db/sqlc"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/mock/gomock"
	"golang.org/x/crypto/bcrypt"
)

// Build-phases.txt 3i-b: the Super Admin's manager oversight, moved here from
// rentals-api. These tests run the real router against a stateful fake of
// willcoll_platform (a gomock store whose manager status, staff and audit
// rows behave like the tables) and an in-memory session store standing in
// for the shared sessions table.
//
// The other half of the suspend/reinstate rule — that a product API then
// refuses the manager's and staff's next request — is rentals-api's
// TestSuspendedFirmIsRefused, which reads the same session shape this file
// writes (package main can't be imported across binaries).

var (
	fakeManagerID = uuid.MustParse("00000000-0000-0000-0000-0000000000c1")
	fakeStaffID   = uuid.MustParse("00000000-0000-0000-0000-0000000000e1")
	fakeAdminID   = uuid.MustParse("00000000-0000-0000-0000-0000000000d1")
)

const (
	fakePassword      = "correct-horse-battery"
	fakeManagerEmail  = "owner@firm.example.com"
	fakeStaffEmail    = "clerk@firm.example.com"
	fakeSessionCookie = "willcoll_session"
)

type fakePlatform struct {
	app    *application
	router *gin.Engine
	store  *mock.MockStore // for tests that add their own table behind the fake

	mu            sync.Mutex
	managerStatus string
	activated     bool
	staffHash     []byte // the staff member's password hash, changed by UpdateStaff
	staffVersion  int32
	staffRole     string   // the staff member's role; officer unless a test sets it
	staffModules  []string // module_access; [rentals] unless a test sets it
	staffProps    []byte   // property_access jsonb; none unless a test sets it
	staffStatus   string   // active unless a test sets it
	firmName      string   // the manager's firm name, changed by UpdateManager
	managerVer    int32
	audit         []sqlc.CreateAuditLogParams
	mail          *captureMailer    // every email the app sent
	outbox        *fakeOutbox       // the email_outbox table
	modules       map[string]string // manager_modules: product -> status (rentals trialing unless a test sets it)
}

// firmActiveModules makes the firm hold exactly these products, all active.
func (f *fakePlatform) firmActiveModules(products ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.modules = map[string]string{}
	for _, p := range products {
		f.modules[p] = data.ModuleStatusActive
	}
}

// deliver runs the outbox worker once (after the handlers' background enqueues
// finished) and returns how many emails it sent.
func (f *fakePlatform) deliver() int {
	f.app.wg.Wait()
	return f.app.deliverOutbox(context.Background())
}

func newFakePlatform(t *testing.T) *fakePlatform {
	t.Helper()
	gin.SetMode(gin.TestMode)

	ctrl := gomock.NewController(t)
	store := mock.NewMockStore(ctrl)
	q := mock.NewMockQuerier(ctrl)
	store.EXPECT().ExecTenantTx(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ uuid.UUID, fn func(sqlc.Querier) error) error { return fn(q) }).
		AnyTimes()

	hash, err := bcrypt.GenerateFromPassword([]byte(fakePassword), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}

	f := &fakePlatform{store: store, managerStatus: data.ManagerStatusActive, activated: true, staffRole: data.RoleOfficer, firmName: "Kimani & Associates", managerVer: 1, mail: &captureMailer{}}
	managerRow := func() sqlc.Manager {
		f.mu.Lock()
		defer f.mu.Unlock()
		activated := time.Now()
		row := sqlc.Manager{
			ID: fakeManagerID, FirmName: f.firmName, Username: "kimani", Email: fakeManagerEmail,
			Phone: "+254700000000", PasswordHash: hash, Status: f.managerStatus, Version: f.managerVer,
		}
		if f.activated {
			row.ActivatedAt = &activated
		}
		return row
	}
	f.staffHash, f.staffVersion = hash, 1
	staffRow := func() sqlc.Staff {
		f.mu.Lock()
		defer f.mu.Unlock()
		modules, status := f.staffModules, f.staffStatus
		if modules == nil {
			modules = []string{"rentals"}
		}
		if status == "" {
			status = data.StaffActive
		}
		return sqlc.Staff{
			ID: fakeStaffID, ManagerID: fakeManagerID, Name: "Clerk", Email: fakeStaffEmail, PasswordHash: f.staffHash,
			Role: f.staffRole, ModuleAccess: modules, Status: status, Version: f.staffVersion, PropertyAccess: f.staffProps,
		}
	}

	// The firm-profile save (PATCH /v1/account): optimistic concurrency on version.
	store.EXPECT().UpdateManager(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, a sqlc.UpdateManagerParams) (int32, error) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if a.ID != fakeManagerID || a.Version != f.managerVer {
			return 0, sql.ErrNoRows
		}
		f.firmName = a.FirmName
		f.managerVer++
		return f.managerVer, nil
	}).AnyTimes()
	store.EXPECT().GetManager(gomock.Any(), fakeManagerID).DoAndReturn(func(context.Context, uuid.UUID) (sqlc.Manager, error) {
		return managerRow(), nil
	}).AnyTimes()
	store.EXPECT().GetManager(gomock.Any(), gomock.Any()).Return(sqlc.Manager{}, sql.ErrNoRows).AnyTimes()
	store.EXPECT().GetManagerByEmail(gomock.Any(), fakeManagerEmail).DoAndReturn(func(context.Context, string) (sqlc.Manager, error) {
		return managerRow(), nil
	}).AnyTimes()
	store.EXPECT().GetManagerByEmail(gomock.Any(), gomock.Any()).Return(sqlc.Manager{}, sql.ErrNoRows).AnyTimes()
	store.EXPECT().GetStaff(gomock.Any(), fakeStaffID).DoAndReturn(func(context.Context, uuid.UUID) (sqlc.Staff, error) {
		return staffRow(), nil
	}).AnyTimes()
	store.EXPECT().ListActiveStaffByEmail(gomock.Any(), fakeStaffEmail).DoAndReturn(func(context.Context, string) ([]sqlc.Staff, error) {
		return []sqlc.Staff{staffRow()}, nil
	}).AnyTimes()
	q.EXPECT().GetStaff(gomock.Any(), fakeStaffID).DoAndReturn(func(context.Context, uuid.UUID) (sqlc.Staff, error) {
		return staffRow(), nil
	}).AnyTimes()
	// An owner's or the staff member's own save: optimistic concurrency on version.
	q.EXPECT().UpdateStaff(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, a sqlc.UpdateStaffParams) (int32, error) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if a.ID != fakeStaffID || a.Version != f.staffVersion {
			return 0, sql.ErrNoRows
		}
		f.staffHash = a.PasswordHash
		f.staffRole, f.staffModules, f.staffProps, f.staffStatus = a.Role, a.ModuleAccess, a.PropertyAccess, a.Status
		f.staffVersion++
		return f.staffVersion, nil
	}).AnyTimes()
	store.EXPECT().ListActiveStaffByEmail(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
	store.EXPECT().GetAdmin(gomock.Any(), fakeAdminID).Return(sqlc.Admin{ID: fakeAdminID, Name: "Root"}, nil).AnyTimes()

	// The oversight reads and the status move.
	overview := func(context.Context, uuid.UUID) (sqlc.OversightGetManagerRow, error) {
		return sqlc.OversightGetManagerRow{Manager: managerRow(), StaffCount: 1, UnitCount: 12}, nil
	}
	store.EXPECT().OversightGetManager(gomock.Any(), fakeManagerID).DoAndReturn(overview).AnyTimes()
	store.EXPECT().OversightGetManager(gomock.Any(), gomock.Any()).Return(sqlc.OversightGetManagerRow{}, sql.ErrNoRows).AnyTimes()
	store.EXPECT().OversightListManagers(gomock.Any(), gomock.Any()).DoAndReturn(
		func(context.Context, sqlc.OversightListManagersParams) ([]sqlc.OversightListManagersRow, error) {
			return []sqlc.OversightListManagersRow{{TotalRecords: 1, Manager: managerRow(), StaffCount: 1, UnitCount: 12}}, nil
		}).AnyTimes()
	store.EXPECT().OversightListModules(gomock.Any(), gomock.Any()).Return([]sqlc.ManagerModule{
		{ManagerID: fakeManagerID, Product: data.ProductRentals, Status: data.ModuleStatusActive},
	}, nil).AnyTimes()
	store.EXPECT().OversightListManagerSubscriptions(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
	store.EXPECT().OversightListUsage(gomock.Any(), gomock.Any()).Return([]sqlc.ModuleUsage{
		{ManagerID: fakeManagerID, Product: data.ProductRentals, BillableCount: 12},
	}, nil).AnyTimes()
	store.EXPECT().OversightListStaffIDs(gomock.Any(), fakeManagerID).Return([]uuid.UUID{fakeStaffID}, nil).AnyTimes()
	store.EXPECT().OversightListSubscriptions(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
	store.EXPECT().SetManagerStatus(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, a sqlc.SetManagerStatusParams) (int32, error) {
			f.mu.Lock()
			defer f.mu.Unlock()
			if a.ID != fakeManagerID || f.managerStatus != a.FromStatus {
				return 0, sql.ErrNoRows
			}
			f.managerStatus = a.ToStatus
			return 2, nil
		}).AnyTimes()
	// Paywall standing for GET /v1/sessions?product=rentals: a running trial.
	trialEnds := time.Now().Add(5 * 24 * time.Hour)
	f.modules = map[string]string{data.ProductRentals: data.ModuleStatusTrialing}
	q.EXPECT().GetManagerModule(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, a sqlc.GetManagerModuleParams) (sqlc.ManagerModule, error) {
			f.mu.Lock()
			defer f.mu.Unlock()
			status, held := f.modules[a.Product]
			if !held {
				return sqlc.ManagerModule{}, sql.ErrNoRows
			}
			return sqlc.ManagerModule{ManagerID: fakeManagerID, Product: a.Product, Status: status, TrialEndsAt: &trialEnds}, nil
		}).AnyTimes()
	q.EXPECT().GetSubscriptionByManagerAndProduct(gomock.Any(), gomock.Any()).Return(sqlc.Subscription{}, sql.ErrNoRows).AnyTimes()
	q.EXPECT().ListSubscriptionPlansForProduct(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
	q.EXPECT().GetModuleUsage(gomock.Any(), gomock.Any()).Return(sqlc.ModuleUsage{BillableCount: 12}, nil).AnyTimes()
	q.EXPECT().UpsertModuleUsage(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, a sqlc.UpsertModuleUsageParams) (sqlc.ModuleUsage, error) {
		return sqlc.ModuleUsage{ManagerID: a.ManagerID, Product: a.Product, BillableCount: a.BillableCount}, nil
	}).AnyTimes()
	q.EXPECT().CreateAuditLog(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, a sqlc.CreateAuditLogParams) error {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.audit = append(f.audit, a)
		return nil
	}).AnyTimes()

	// The owner-alert outbox (C3.7): enqueue in a tenant transaction, claim and
	// settle on the admin store (the same mock here).
	f.outbox = newFakeOutbox()
	q.EXPECT().EnqueueEmail(gomock.Any(), gomock.Any()).DoAndReturn(f.outbox.enqueue).AnyTimes()
	store.EXPECT().ClaimDueEmails(gomock.Any(), gomock.Any()).DoAndReturn(f.outbox.claim).AnyTimes()
	store.EXPECT().MarkEmailSent(gomock.Any(), gomock.Any()).DoAndReturn(f.outbox.markSent).AnyTimes()
	store.EXPECT().MarkEmailFailed(gomock.Any(), gomock.Any()).DoAndReturn(f.outbox.markFailed).AnyTimes()
	store.EXPECT().OutboxStats(gomock.Any()).DoAndReturn(f.outbox.stats).AnyTimes()

	sm := scs.New()
	sm.Store = memstore.New()
	sm.Cookie.Name = fakeSessionCookie

	f.app = &application{
		logger:         slog.New(slog.NewTextHandler(io.Discard, nil)),
		models:         data.NewModelsFromStore(store, 0),
		sessionManager: sm,
		mailer:         f.mail,
	}
	f.app.config.cors.trustedOrigins = []string{"http://localhost:3000"}
	router, err := f.app.routes()
	if err != nil {
		t.Fatal(err)
	}
	f.router = router
	return f
}

func (f *fakePlatform) do(method, target, body string, cookie *http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if cookie != nil {
		req.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	f.router.ServeHTTP(w, req)
	return w
}

// session writes a session for principal straight into the store, the row
// login would leave behind.
func (f *fakePlatform) session(t *testing.T, principalType string, id uuid.UUID) *http.Cookie {
	t.Helper()
	sm := f.app.sessionManager
	ctx, err := sm.Load(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	sm.Put(ctx, sessionKeyPrincipalType, principalType)
	sm.Put(ctx, sessionKeyPrincipalID, id.String())
	token, _, err := sm.Commit(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return &http.Cookie{Name: fakeSessionCookie, Value: token}
}

// login signs in through the real endpoint and returns the cookie it set.
func (f *fakePlatform) login(t *testing.T, email string) (*http.Cookie, int) {
	t.Helper()
	w := f.do(http.MethodPost, "/v1/sessions", `{"email":"`+email+`","password":"`+fakePassword+`"}`, nil)
	for _, c := range w.Result().Cookies() {
		if c.Name == fakeSessionCookie && c.Value != "" {
			return c, w.Code
		}
	}
	return nil, w.Code
}

func (f *fakePlatform) auditActions() []string {
	f.app.wg.Wait() // audit rows are written in the background
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, a := range f.audit {
		out = append(out, a.ActorType+":"+a.Action)
	}
	return out
}

func TestAdminManagerRoutesRequireAdmin(t *testing.T) {
	f := newFakePlatform(t)
	managerCookie := f.session(t, principalManager, fakeManagerID)
	staffCookie := f.session(t, principalStaff, fakeStaffID)
	id := fakeManagerID.String()

	routes := []struct{ method, path string }{
		{"GET", "/v1/admin/managers"},
		{"GET", "/v1/admin/managers/" + id},
		{"POST", "/v1/admin/managers/" + id + "/suspend"},
		{"POST", "/v1/admin/managers/" + id + "/reinstate"},
		{"GET", "/v1/admin/subscriptions"},
	}
	for _, r := range routes {
		t.Run(r.method+" "+r.path, func(t *testing.T) {
			if w := f.do(r.method, r.path, "", nil); w.Code != http.StatusUnauthorized {
				t.Errorf("anonymous: %d, want 401", w.Code)
			}
			if w := f.do(r.method, r.path, "", managerCookie); w.Code != http.StatusForbidden {
				t.Errorf("manager: %d, want 403", w.Code)
			}
			if w := f.do(r.method, r.path, "", staffCookie); w.Code != http.StatusForbidden {
				t.Errorf("staff: %d, want 403", w.Code)
			}
		})
	}

	// A refused non-admin must not have changed anything.
	if f.managerStatus != data.ManagerStatusActive {
		t.Errorf("a non-admin suspend request changed the firm's status to %q", f.managerStatus)
	}
	if got := f.auditActions(); len(got) != 0 {
		t.Errorf("refused requests wrote audit rows: %v", got)
	}
}

func TestAdminReadManagers(t *testing.T) {
	f := newFakePlatform(t)
	admin := f.session(t, principalAdmin, fakeAdminID)

	var list struct {
		Managers []struct {
			ID         uuid.UUID `json:"id"`
			FirmName   string    `json:"firm_name"`
			Status     string    `json:"status"`
			StaffCount int       `json:"staff_count"`
			UnitCount  int       `json:"unit_count"`
			Modules    []struct {
				Product string `json:"product"`
				Status  string `json:"status"`
			} `json:"modules"`
			Usage []struct {
				Product       string `json:"product"`
				BillableCount int    `json:"billable_count"`
			} `json:"usage"`
			Subscription  *struct{} `json:"subscription"`
			Subscriptions []any     `json:"subscriptions"`
		} `json:"managers"`
		Metadata struct {
			TotalRecords int `json:"total_records"`
		} `json:"metadata"`
	}
	w := f.do(http.MethodGet, "/v1/admin/managers?status=active&sort=firm_name", "", admin)
	if w.Code != http.StatusOK {
		t.Fatalf("list: %d %s", w.Code, w.Body)
	}
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Managers) != 1 || list.Metadata.TotalRecords != 1 {
		t.Fatalf("list = %+v", list)
	}
	m := list.Managers[0]
	if m.ID != fakeManagerID || m.FirmName != "Kimani & Associates" || m.Status != "active" || m.StaffCount != 1 || m.UnitCount != 12 {
		t.Errorf("manager overview = %+v", m)
	}
	if len(m.Modules) != 1 || m.Modules[0].Product != "rentals" || len(m.Usage) != 1 || m.Usage[0].BillableCount != 12 {
		t.Errorf("modules/usage = %+v / %+v", m.Modules, m.Usage)
	}
	if m.Subscription != nil || m.Subscriptions == nil {
		t.Errorf("no subscription yet: subscription=%v subscriptions=%v (want null and [])", m.Subscription, m.Subscriptions)
	}

	if w := f.do(http.MethodGet, "/v1/admin/managers/"+fakeManagerID.String(), "", admin); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"staff_count": 1`) {
		t.Errorf("show: %d %s", w.Code, w.Body)
	}
	if w := f.do(http.MethodGet, "/v1/admin/managers/"+uuid.NewString(), "", admin); w.Code != http.StatusNotFound {
		t.Errorf("show unknown: %d, want 404", w.Code)
	}
	if w := f.do(http.MethodGet, "/v1/admin/managers/not-a-uuid", "", admin); w.Code != http.StatusNotFound {
		t.Errorf("show bad id: %d, want 404", w.Code)
	}
	if w := f.do(http.MethodGet, "/v1/admin/managers?status=nonsense", "", admin); w.Code != http.StatusUnprocessableEntity {
		t.Errorf("bad status filter: %d, want 422", w.Code)
	}
	if w := f.do(http.MethodGet, "/v1/admin/managers?sort=password", "", admin); w.Code != http.StatusUnprocessableEntity {
		t.Errorf("bad sort: %d, want 422", w.Code)
	}
}

func TestAdminListSubscriptionsFilters(t *testing.T) {
	f := newFakePlatform(t)
	admin := f.session(t, principalAdmin, fakeAdminID)

	for _, target := range []string{
		"/v1/admin/subscriptions",
		"/v1/admin/subscriptions?product=rentals&status=active",
		"/v1/admin/subscriptions?product=estates&status=past_due&sort=-current_period_end&page=2&page_size=5",
	} {
		if w := f.do(http.MethodGet, target, "", admin); w.Code != http.StatusOK {
			t.Errorf("GET %s: %d %s, want 200", target, w.Code, w.Body)
		}
	}
	for _, target := range []string{
		"/v1/admin/subscriptions?product=billing",
		"/v1/admin/subscriptions?status=paused",
		"/v1/admin/subscriptions?sort=price",
		"/v1/admin/subscriptions?page_size=1000",
	} {
		if w := f.do(http.MethodGet, target, "", admin); w.Code != http.StatusUnprocessableEntity {
			t.Errorf("GET %s: %d, want 422", target, w.Code)
		}
	}
}

// TestSuspendAndReinstate is the 3i-b acceptance flow on platform-api's side:
// suspending a firm destroys the manager's AND their staff's sessions at
// once, writes admin audit rows, stops both signing in again (so every
// product API — which reads the same sessions table and the same manager
// status — refuses them), and a reinstatement lets both back in.
func TestSuspendAndReinstate(t *testing.T) {
	f := newFakePlatform(t)
	admin := f.session(t, principalAdmin, fakeAdminID)
	suspend := "/v1/admin/managers/" + fakeManagerID.String() + "/suspend"
	reinstate := "/v1/admin/managers/" + fakeManagerID.String() + "/reinstate"

	// Both principals sign in through the real endpoint and use the API.
	managerCookie, code := f.login(t, fakeManagerEmail)
	if code != http.StatusOK {
		t.Fatalf("manager login: %d", code)
	}
	staffCookie, code := f.login(t, fakeStaffEmail)
	if code != http.StatusOK {
		t.Fatalf("staff login: %d", code)
	}
	// A second device for the manager: every session goes, not just one.
	managerPhone, _ := f.login(t, fakeManagerEmail)
	for name, c := range map[string]*http.Cookie{"manager": managerCookie, "staff": staffCookie, "phone": managerPhone} {
		if w := f.do(http.MethodGet, "/v1/account", "", c); w.Code != http.StatusOK {
			t.Fatalf("%s before suspension: %d %s, want 200", name, w.Code, w.Body)
		}
	}

	// An unrelated session must survive: the admin's own.
	w := f.do(http.MethodPost, suspend, "", admin)
	if w.Code != http.StatusOK {
		t.Fatalf("suspend: %d %s", w.Code, w.Body)
	}
	var out struct {
		Manager         struct{ Status string } `json:"manager"`
		SessionsRevoked int                     `json:"sessions_revoked"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Manager.Status != data.ManagerStatusSuspended || out.SessionsRevoked != 3 {
		t.Errorf("suspend response = %+v, want suspended and 3 sessions revoked (manager x2 + staff)", out)
	}

	// Their next requests: the session rows are gone.
	for name, c := range map[string]*http.Cookie{"manager": managerCookie, "staff": staffCookie, "phone": managerPhone} {
		if w := f.do(http.MethodGet, "/v1/account", "", c); w.Code != http.StatusUnauthorized {
			t.Errorf("%s after suspension: %d, want 401", name, w.Code)
		}
	}
	if w := f.do(http.MethodGet, "/v1/admin/managers", "", admin); w.Code != http.StatusOK {
		t.Errorf("the admin's own session was revoked too: %d", w.Code)
	}

	// Even a session minted behind platform-api's back (a race, a bug) is
	// refused on status, for the manager and for staff.
	for name, c := range map[string]*http.Cookie{
		"manager": f.session(t, principalManager, fakeManagerID),
		"staff":   f.session(t, principalStaff, fakeStaffID),
	} {
		if w := f.do(http.MethodGet, "/v1/account", "", c); w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "suspended") {
			t.Errorf("%s with a surviving session: %d %s, want 403 suspended", name, w.Code, w.Body)
		}
	}

	// And neither can sign in again.
	if c, code := f.login(t, fakeManagerEmail); code != http.StatusForbidden || c != nil {
		t.Errorf("suspended manager login: %d (cookie %v), want 403 and no session", code, c)
	}
	if c, code := f.login(t, fakeStaffEmail); code != http.StatusForbidden || c != nil {
		t.Errorf("suspended firm's staff login: %d (cookie %v), want 403 and no session", code, c)
	}

	// Suspending again is harmless, and sweeps up the two sessions that
	// slipped in behind its back above.
	if w := f.do(http.MethodPost, suspend, "", admin); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"sessions_revoked": 2`) {
		t.Errorf("second suspend: %d %s", w.Code, w.Body)
	}
	if w := f.do(http.MethodPost, suspend, "", admin); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"sessions_revoked": 0`) {
		t.Errorf("third suspend: %d %s", w.Code, w.Body)
	}

	// Reinstate: they can sign in and use the API again.
	if w := f.do(http.MethodPost, reinstate, "", admin); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"status": "active"`) {
		t.Fatalf("reinstate: %d %s", w.Code, w.Body)
	}
	managerCookie, code = f.login(t, fakeManagerEmail)
	if code != http.StatusOK {
		t.Fatalf("manager login after reinstatement: %d", code)
	}
	staffCookie, code = f.login(t, fakeStaffEmail)
	if code != http.StatusOK {
		t.Fatalf("staff login after reinstatement: %d", code)
	}
	for name, c := range map[string]*http.Cookie{"manager": managerCookie, "staff": staffCookie} {
		if w := f.do(http.MethodGet, "/v1/account", "", c); w.Code != http.StatusOK {
			t.Errorf("%s after reinstatement: %d %s, want 200", name, w.Code, w.Body)
		}
	}

	// Every state change is on the audit trail, as the admin.
	want := []string{"admin:manager.suspended", "admin:manager.suspended", "admin:manager.suspended", "admin:manager.reinstated"}
	got := f.auditActions()
	// Written by background goroutines, so compare as sets, not in order.
	sort.Strings(got)
	sort.Strings(want)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("audit trail = %v, want %v", got, want)
	}
	for _, a := range f.audit {
		if a.ManagerID != fakeManagerID || a.ActorID == nil || *a.ActorID != fakeAdminID {
			t.Errorf("audit row not scoped to the firm / attributed to the admin: %+v", a)
		}
	}

	// Unknown firm.
	missing := "/v1/admin/managers/" + uuid.NewString()
	if w := f.do(http.MethodPost, missing+"/suspend", "", admin); w.Code != http.StatusNotFound {
		t.Errorf("suspend unknown: %d, want 404", w.Code)
	}
	if w := f.do(http.MethodPost, missing+"/reinstate", "", admin); w.Code != http.StatusNotFound {
		t.Errorf("reinstate unknown: %d, want 404", w.Code)
	}
}

// A firm that never activated goes back to pending, not active, so a
// reinstatement can never skip activation.
func TestReinstateNeverActivatedFirmGoesToPending(t *testing.T) {
	f := newFakePlatform(t)
	f.managerStatus, f.activated = data.ManagerStatusPending, false
	admin := f.session(t, principalAdmin, fakeAdminID)

	suspend := "/v1/admin/managers/" + fakeManagerID.String() + "/suspend"
	if w := f.do(http.MethodPost, suspend, "", admin); w.Code != http.StatusOK {
		t.Fatalf("suspend: %d %s", w.Code, w.Body)
	}
	w := f.do(http.MethodPost, "/v1/admin/managers/"+fakeManagerID.String()+"/reinstate", "", admin)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"status": "pending"`) {
		t.Errorf("reinstate: %d %s, want pending", w.Code, w.Body)
	}
}

// GET /v1/sessions?product=rentals carries the firm's paywall standing for that
// product (the trial banner), and only when asked.
func TestSessionCarriesAccessForAProduct(t *testing.T) {
	f := newFakePlatform(t)
	cookie := f.session(t, principalManager, fakeManagerID)

	var plain map[string]json.RawMessage
	w := f.do(http.MethodGet, "/v1/sessions", "", cookie)
	if w.Code != http.StatusOK {
		t.Fatalf("session: %d %s", w.Code, w.Body)
	}
	_ = json.Unmarshal(w.Body.Bytes(), &plain)
	if _, ok := plain["access"]; ok {
		t.Error("access should only appear when a product is asked for")
	}

	w = f.do(http.MethodGet, "/v1/sessions?product=rentals", "", cookie)
	var out struct {
		Access struct {
			State    string `json:"state"`
			DaysLeft int    `json:"days_left"`
		} `json:"access"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || w.Code != http.StatusOK {
		t.Fatalf("session?product: %d %s (%v)", w.Code, w.Body, err)
	}
	if out.Access.State != "trial" || out.Access.DaysLeft < 4 || out.Access.DaysLeft > 6 {
		t.Errorf("access = %+v, want a running trial of about 5 days", out.Access)
	}

	if w := f.do(http.MethodGet, "/v1/sessions?product=bogus", "", cookie); w.Code != http.StatusUnprocessableEntity {
		t.Errorf("unknown product: %d, want 422", w.Code)
	}

	// The plans route reports the firm's unit count for the plan picker.
	w = f.do(http.MethodGet, "/v1/billing/rentals/plans", "", cookie)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"unit_count": 12`) {
		t.Errorf("plans: %d %s, want unit_count 12", w.Code, w.Body)
	}
}
