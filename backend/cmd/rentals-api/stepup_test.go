package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	platformdata "github.com/codercollo/willcoll/backend/internal/platform/data"
	"github.com/codercollo/willcoll/backend/internal/rentals/db/mock"
	"github.com/codercollo/willcoll/backend/internal/rentals/db/sqlc"
	"github.com/codercollo/willcoll/backend/internal/shared/auth"
	"github.com/codercollo/willcoll/backend/internal/shared/mailer"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/mock/gomock"
)

// Step-up (authorization.md D5) as rentals-api sees it: platform-api opens
// the window in the shared session; these gates read it, and the owner is
// emailed about channel changes and reversals.

type sentMail struct {
	Recipient string
	Template  string
	Data      map[string]any
}

// captureMailer records every email instead of sending it.
type captureMailer struct {
	mu   sync.Mutex
	sent []sentMail
}

func (m *captureMailer) Send(recipient, templateFile string, data any) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, _ := data.(map[string]any)
	m.sent = append(m.sent, sentMail{Recipient: recipient, Template: templateFile, Data: d})
	return nil
}

func (m *captureMailer) alerts() []sentMail {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []sentMail
	for _, s := range m.sent {
		if s.Template == mailer.SecurityAlertTemplate {
			out = append(out, s)
		}
	}
	return out
}

// steppedCookie is sessionCookie plus a step-up window ending at until, as
// platform-api's POST /v1/sessions/step-up would leave it (0 = none).
func (m *middlewareTestApp) steppedCookie(t *testing.T, values map[string]string, until int64) *http.Cookie {
	t.Helper()
	sm := m.app.platformSessionManager
	ctx, err := sm.Load(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range values {
		sm.Put(ctx, k, v)
	}
	if until != 0 {
		sm.Put(ctx, auth.SessionKeyStepUpUntil, until)
	}
	token, _, err := sm.Commit(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return &http.Cookie{Name: sm.Cookie.Name, Value: token}
}

func (m *middlewareTestApp) post(target, body string, cookie *http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, target, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	m.router.ServeHTTP(w, req)
	return w
}

func stepUpCode(t *testing.T, body string) string {
	t.Helper()
	var out struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal([]byte(body), &out) // a non-JSON body just has no code
	return out.Code
}

var (
	firmLogin = map[string]string{sessionKeyPrincipalType: principalManager, sessionKeyPrincipalID: testManagerID.String()}

	openWindow   = func() int64 { return auth.StepUpUntil(time.Now()) }
	closedWindow = func() int64 { return time.Now().Add(-time.Second).Unix() }
)

// The three always-gated routes. A bad id 404s from the handler itself, so a
// 404 proves the request got past every gate without any database call.
var stepUpRoutes = []struct{ name, target string }{
	{"reverse a ledger entry", "/v1/ledger-entries/not-a-uuid/reverse"},
	{"register a payment channel", "/v1/properties/not-a-uuid/payment-channel"},
	{"send a payment channel test", "/v1/properties/not-a-uuid/payment-channel/test"},
}

func TestMoneyActionsNeedStepUp(t *testing.T) {
	for _, route := range stepUpRoutes {
		t.Run(route.name, func(t *testing.T) {
			m := newMiddlewareTestApp(t)
			m.pStore.EXPECT().GetManager(gomock.Any(), testManagerID).Return(managerRow(platformdata.ManagerStatusActive), nil).AnyTimes()

			for name, tc := range map[string]struct {
				until    int64
				wantCode int
				wantStep bool
			}{
				"no window":      {0, http.StatusForbidden, true},
				"expired window": {closedWindow(), http.StatusForbidden, true},
				"open window":    {openWindow(), http.StatusNotFound, false},
			} {
				w := m.post(route.target, `{}`, m.steppedCookie(t, firmLogin, tc.until))
				if w.Code != tc.wantCode || (stepUpCode(t, w.Body.String()) == auth.StepUpCode) != tc.wantStep {
					t.Errorf("%s: status %d body %s; want %d (step-up prompt: %v)", name, w.Code, w.Body, tc.wantCode, tc.wantStep)
				}
			}
		})
	}
}

// The permission check comes first: a caller who may not do the action at all
// gets the plain 403, not a password prompt that could never help.
func TestStepUpPromptComesAfterPermission(t *testing.T) {
	cases := []struct {
		role   string
		target string
	}{
		{platformdata.RoleOfficer, "/v1/ledger-entries/not-a-uuid/reverse"},             // reversal needs accountant
		{platformdata.RoleAccountant, "/v1/properties/not-a-uuid/payment-channel"},      // channels need owner
		{platformdata.RoleAccountant, "/v1/properties/not-a-uuid/payment-channel/test"}, // channels need owner
	}
	for _, tc := range cases {
		t.Run(tc.role+" "+tc.target, func(t *testing.T) {
			m := newMiddlewareTestApp(t)
			staff := testStaffRow(tc.role, "rentals")
			m.pStore.EXPECT().GetStaff(gomock.Any(), staff.ID).Return(staff, nil).AnyTimes()
			m.pStore.EXPECT().GetManager(gomock.Any(), testManagerID).Return(managerRow(platformdata.ManagerStatusActive), nil).AnyTimes()
			cookie := m.steppedCookie(t, map[string]string{sessionKeyPrincipalType: principalStaff, sessionKeyPrincipalID: staff.ID.String()}, 0)

			w := m.post(tc.target, `{}`, cookie)
			if w.Code != http.StatusForbidden || stepUpCode(t, w.Body.String()) == auth.StepUpCode {
				t.Errorf("status %d body %s; want a plain 403 (permission)", w.Code, w.Body)
			}
		})
	}
}

// An accountant may reverse, so they are the one asked for a password.
func TestAccountantReversalIsAskedForPassword(t *testing.T) {
	m := newMiddlewareTestApp(t)
	staff := testStaffRow(platformdata.RoleAccountant, "rentals")
	m.pStore.EXPECT().GetStaff(gomock.Any(), staff.ID).Return(staff, nil).AnyTimes()
	m.pStore.EXPECT().GetManager(gomock.Any(), testManagerID).Return(managerRow(platformdata.ManagerStatusActive), nil).AnyTimes()
	session := map[string]string{sessionKeyPrincipalType: principalStaff, sessionKeyPrincipalID: staff.ID.String()}

	w := m.post("/v1/ledger-entries/not-a-uuid/reverse", `{}`, m.steppedCookie(t, session, 0))
	if w.Code != http.StatusForbidden || stepUpCode(t, w.Body.String()) != auth.StepUpCode {
		t.Errorf("status %d body %s; want 403 %s", w.Code, w.Body, auth.StepUpCode)
	}
	if w := m.post("/v1/ledger-entries/not-a-uuid/reverse", `{}`, m.steppedCookie(t, session, openWindow())); w.Code != http.StatusNotFound {
		t.Errorf("with an open window: status %d, want 404 (into the handler)", w.Code)
	}
}

// A window belongs to the session that opened it.
func TestStepUpWindowIsPerSession(t *testing.T) {
	m := newMiddlewareTestApp(t)
	m.pStore.EXPECT().GetManager(gomock.Any(), testManagerID).Return(managerRow(platformdata.ManagerStatusActive), nil).AnyTimes()
	_ = m.steppedCookie(t, firmLogin, openWindow()) // someone else's session, stepped up
	other := m.steppedCookie(t, firmLogin, 0)

	w := m.post("/v1/ledger-entries/not-a-uuid/reverse", `{}`, other)
	if w.Code != http.StatusForbidden || stepUpCode(t, w.Body.String()) != auth.StepUpCode {
		t.Errorf("status %d body %s; want 403 %s", w.Code, w.Body, auth.StepUpCode)
	}
}

// --- the channel field on POST/PATCH /properties ---

// serveWithoutStepUp runs handler as the firm login with no step-up window.
func serveWithoutStepUp(handler gin.HandlerFunc, method, body string, params gin.Params) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(method, "/", strings.NewReader(body))
	c.Params = params
	contextSetTenantID(c, testTenantID)
	contextSetManager(c, &platformdata.Manager{ID: testTenantID})
	handler(c)
	return w
}

func TestChannelChangeOnPropertiesNeedsStepUp(t *testing.T) {
	params := gin.Params{{Key: "id", Value: testPropertyID.String()}}

	t.Run("create with a channel", func(t *testing.T) {
		app, _ := newPropertyTestApp(t) // strict: a refusal must not touch the database
		body := `{"landlord_id":"` + testLandlordID.String() + `","name":"Runda Arcade","location":"Runda","slug":"runda-arcade","payhero_channel_id":"1234"}`
		w := serveWithoutStepUp(app.createPropertyHandler, http.MethodPost, body, nil)
		if w.Code != http.StatusForbidden || stepUpCode(t, w.Body.String()) != auth.StepUpCode {
			t.Errorf("status %d body %s; want 403 %s", w.Code, w.Body, auth.StepUpCode)
		}
	})

	for name, body := range map[string]string{
		"change the channel": `{"payhero_channel_id":"9999"}`,
		"clear the channel":  `{"payhero_channel_id":""}`,
	} {
		t.Run("patch: "+name, func(t *testing.T) {
			app, q := newPropertyTestApp(t)
			q.EXPECT().GetProperty(gomock.Any(), gomock.Any()).Return(propertyRowWithChannel("1234"), nil)
			w := serveWithoutStepUp(app.updatePropertyHandler, http.MethodPatch, body, params)
			if w.Code != http.StatusForbidden || stepUpCode(t, w.Body.String()) != auth.StepUpCode {
				t.Errorf("status %d body %s; want 403 %s", w.Code, w.Body, auth.StepUpCode)
			}
		})
	}

	// Not a change: no prompt. The stubbed version conflict (409) proves the
	// request went on to the save.
	for name, body := range map[string]string{
		"rename only":        `{"name":"Kiwi Place"}`,
		"echo the channel":   `{"name":"Kiwi Place","payhero_channel_id":"1234"}`,
		"no channel in body": `{"management_fee_percent":4}`,
	} {
		t.Run("patch: "+name+" needs no step-up", func(t *testing.T) {
			app, q := newPropertyTestApp(t)
			q.EXPECT().GetProperty(gomock.Any(), gomock.Any()).Return(propertyRowWithChannel("1234"), nil)
			q.EXPECT().LandlordBelongsToTenant(gomock.Any(), gomock.Any()).Return(true, nil)
			q.EXPECT().UpdateProperty(gomock.Any(), gomock.Any()).
				Return(int32(0), context.DeadlineExceeded).MaxTimes(1) // any outcome: only "not step-up" matters
			w := serveWithoutStepUp(app.updatePropertyHandler, http.MethodPatch, body, params)
			if stepUpCode(t, w.Body.String()) == auth.StepUpCode {
				t.Errorf("an unchanged channel asked for a password: %d %s", w.Code, w.Body)
			}
		})
	}
}

// --- owner alerts ---

func TestReversalAlertsTheOwner(t *testing.T) {
	m := newMiddlewareTestApp(t)
	mail := &captureMailer{}
	m.app.mailer = mail
	m.pStore.EXPECT().GetManager(gomock.Any(), testManagerID).Return(managerRow(platformdata.ManagerStatusActive), nil).AnyTimes()
	m.pq.EXPECT().CreateAuditLog(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()

	entryID := uuid.New()
	q := mock.NewMockQuerier(gomock.NewController(t))
	m.store.EXPECT().ExecTenantTx(gomock.Any(), testManagerID, gomock.Any()).
		DoAndReturn(func(_ context.Context, _ uuid.UUID, fn func(sqlc.Querier) error) error { return fn(q) }).AnyTimes()
	q.EXPECT().GetLedgerEntryForReversal(gomock.Any(), gomock.Any()).Return(sqlc.GetLedgerEntryForReversalRow{
		ID: entryID, LedgerAccountID: uuid.New(), Direction: "DEBIT", Amount: money("12000"),
		ReferenceType: "CHARGE", ReferenceID: uuid.New(), UnitID: uuid.New(), LedgerType: "RENT",
	}, nil)
	q.EXPECT().CreateTransactionHeader(gomock.Any(), gomock.Any()).Return(uuid.New(), nil)
	q.EXPECT().CreateLedgerEntry(gomock.Any(), gomock.Any()).Return(uuid.New(), nil)

	// Refused first (no window): nothing happens, nobody is alerted. The strict
	// store mock would fail the test on any database call.
	if w := m.post("/v1/ledger-entries/"+entryID.String()+"/reverse", `{"reason":"duplicate"}`, m.steppedCookie(t, firmLogin, 0)); w.Code != http.StatusForbidden {
		t.Fatalf("without a window: status %d, want 403", w.Code)
	}
	m.app.wg.Wait()
	if n := len(mail.alerts()); n != 0 {
		t.Fatalf("%d alerts for a refused reversal, want 0", n)
	}

	w := m.post("/v1/ledger-entries/"+entryID.String()+"/reverse", `{"reason":"duplicate rent charge"}`, m.steppedCookie(t, firmLogin, openWindow()))
	if w.Code != http.StatusCreated {
		t.Fatalf("with a window: status %d body %s, want 201", w.Code, w.Body)
	}
	m.app.wg.Wait()

	alerts := mail.alerts()
	if len(alerts) != 1 {
		t.Fatalf("%d alerts, want 1", len(alerts))
	}
	a := alerts[0]
	if a.Recipient != "firm@example.com" {
		t.Errorf("alert went to %q, want the firm login", a.Recipient)
	}
	for key, want := range map[string]string{
		"Headline": "A ledger entry was reversed",
		"Actor":    "Firm (firm login)",
		"Target":   "KES 12,000.00",
	} {
		if got, _ := a.Data[key].(string); !strings.Contains(got, want) {
			t.Errorf("alert %s = %q, want it to contain %q", key, got, want)
		}
	}
	if got, _ := a.Data["Target"].(string); !strings.Contains(got, entryID.String()) || !strings.Contains(got, "duplicate rent charge") {
		t.Errorf("alert target %q lacks the entry id or the reason", got)
	}
	for _, key := range []string{"IP", "When"} {
		if got, _ := a.Data[key].(string); got == "" {
			t.Errorf("alert %s is empty", key)
		}
	}
}

func TestChannelChangeTarget(t *testing.T) {
	for _, tc := range []struct{ old, new, want string }{
		{"", "1234", "Runda Arcade: channel none to 1234"},
		{"1234", "", "Runda Arcade: channel 1234 to none"},
		{"1234", "5678", "Runda Arcade: channel 1234 to 5678"},
	} {
		if got := channelChangeTarget("Runda Arcade", tc.old, tc.new); got != tc.want {
			t.Errorf("channelChangeTarget(%q, %q) = %q, want %q", tc.old, tc.new, got, tc.want)
		}
	}
}
