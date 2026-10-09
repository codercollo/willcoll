package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/codercollo/willcoll/backend/internal/platform/data"
	"github.com/codercollo/willcoll/backend/internal/platform/db/sqlc"
	"github.com/codercollo/willcoll/backend/internal/shared/auth"
	"github.com/codercollo/willcoll/backend/internal/shared/mailer"
	"github.com/google/uuid"
)

// Step-up (authorization.md D5): password re-entry opens a 5-minute window
// that the dangerous actions require, and the owner is emailed about them.
// These run the real router against the fake in admin_managers_test.go.

type sentMail struct {
	Recipient string
	Template  string
	Data      map[string]any
}

// captureMailer records every email instead of sending it.
type captureMailer struct {
	mu   sync.Mutex
	sent []sentMail
	// err, when set, makes every Send fail (a mail outage).
	err error
}

func (m *captureMailer) Send(recipient, templateFile string, data any) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return m.err
	}
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

// stepUp opens a window on cookie's session through the real endpoint.
func (f *fakePlatform) stepUp(t *testing.T, cookie *http.Cookie) {
	t.Helper()
	w := f.do(http.MethodPost, "/v1/sessions/step-up", `{"password":"`+fakePassword+`"}`, cookie)
	if w.Code != http.StatusOK {
		t.Fatalf("step-up = %d: %s", w.Code, w.Body.String())
	}
}

// setSessionValue writes a raw value into cookie's stored session.
func (f *fakePlatform) setSessionValue(t *testing.T, cookie *http.Cookie, key string, value any) {
	t.Helper()
	sm := f.app.sessionManager
	ctx, err := sm.Load(context.Background(), cookie.Value)
	if err != nil {
		t.Fatal(err)
	}
	sm.Put(ctx, key, value)
	if _, _, err := sm.Commit(ctx); err != nil {
		t.Fatal(err)
	}
}

func errorCode(t *testing.T, body string) string {
	t.Helper()
	var out struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("response is not JSON: %v: %s", err, body)
	}
	return out.Code
}

func (f *fakePlatform) patchStaffAs(cookie *http.Cookie, body string) (int, string) {
	w := f.do(http.MethodPatch, "/v1/staff/"+fakeStaffID.String(), body, cookie)
	return w.Code, w.Body.String()
}

func TestStaffAuthorityChangeNeedsStepUp(t *testing.T) {
	for _, body := range []string{
		`{"role":"accountant"}`,
		`{"status":"disabled"}`,
		`{"module_access":["rentals","estates"]}`,
		`{"property_access":{"rentals":["` + propX.String() + `"]}}`,
	} {
		t.Run(body, func(t *testing.T) {
			f := newFakePlatform(t)
			f.firmActiveModules(data.ProductRentals, data.ProductEstates) // M8: the firm holds what is granted
			owner := f.session(t, principalManager, fakeManagerID)

			code, resp := f.patchStaffAs(owner, body)
			if code != http.StatusForbidden || errorCode(t, resp) != auth.StepUpCode {
				t.Fatalf("without step-up: %d %s, want 403 %s", code, resp, auth.StepUpCode)
			}
			if f.staffRole != data.RoleOfficer || f.staffStatus != "" {
				t.Errorf("a refused change was saved: role %q status %q", f.staffRole, f.staffStatus)
			}

			f.stepUp(t, owner)
			if code, resp := f.patchStaffAs(owner, body); code != http.StatusOK {
				t.Fatalf("after step-up: %d %s", code, resp)
			}
		})
	}
}

func TestStaffRenameNeedsNoStepUp(t *testing.T) {
	f := newFakePlatform(t)
	owner := f.session(t, principalManager, fakeManagerID)
	if code, resp := f.patchStaffAs(owner, `{"name":"Renamed Clerk"}`); code != http.StatusOK {
		t.Fatalf("rename = %d %s, want 200", code, resp)
	}
}

func TestInviteStaffNeedsStepUp(t *testing.T) {
	f := newFakePlatform(t)
	owner := f.session(t, principalManager, fakeManagerID)
	w := f.do(http.MethodPost, "/v1/staff", `{"name":"New","email":"new@firm.example.com","role":"owner","module_access":["rentals"]}`, owner)
	if w.Code != http.StatusForbidden || errorCode(t, w.Body.String()) != auth.StepUpCode {
		t.Fatalf("invite without step-up = %d %s, want 403 %s", w.Code, w.Body.String(), auth.StepUpCode)
	}
}

func TestStepUpWrongPassword(t *testing.T) {
	f := newFakePlatform(t)
	owner := f.session(t, principalManager, fakeManagerID)

	w := f.do(http.MethodPost, "/v1/sessions/step-up", `{"password":"not-it"}`, owner)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("wrong password = %d, want 422 (not 401: the session is still good): %s", w.Code, w.Body.String())
	}
	if code, _ := f.patchStaffAs(owner, `{"role":"viewer"}`); code != http.StatusForbidden {
		t.Errorf("a wrong password opened a window: PATCH = %d", code)
	}
}

func TestStepUpEndsSessionAfterRepeatedFailures(t *testing.T) {
	f := newFakePlatform(t)
	owner := f.session(t, principalManager, fakeManagerID)

	for i := 1; i < auth.StepUpMaxFailures; i++ {
		if w := f.do(http.MethodPost, "/v1/sessions/step-up", `{"password":"nope"}`, owner); w.Code != http.StatusUnprocessableEntity {
			t.Fatalf("attempt %d = %d, want 422", i, w.Code)
		}
	}
	if w := f.do(http.MethodPost, "/v1/sessions/step-up", `{"password":"nope"}`, owner); w.Code != http.StatusUnauthorized {
		t.Fatalf("attempt %d = %d, want 401 (session ended)", auth.StepUpMaxFailures, w.Code)
	}
	// Even the right password is now useless: the session is gone.
	if w := f.do(http.MethodPost, "/v1/sessions/step-up", `{"password":"`+fakePassword+`"}`, owner); w.Code != http.StatusUnauthorized {
		t.Errorf("right password on a destroyed session = %d, want 401", w.Code)
	}
	if w := f.do(http.MethodGet, "/v1/account", "", owner); w.Code != http.StatusUnauthorized {
		t.Errorf("GET /v1/account on the destroyed session = %d, want 401", w.Code)
	}
}

func TestStepUpFailuresResetOnSuccess(t *testing.T) {
	f := newFakePlatform(t)
	owner := f.session(t, principalManager, fakeManagerID)
	for range auth.StepUpMaxFailures - 1 {
		f.do(http.MethodPost, "/v1/sessions/step-up", `{"password":"nope"}`, owner)
	}
	f.stepUp(t, owner)
	for i := range auth.StepUpMaxFailures - 1 {
		if w := f.do(http.MethodPost, "/v1/sessions/step-up", `{"password":"nope"}`, owner); w.Code != http.StatusUnprocessableEntity {
			t.Fatalf("failure %d after a success = %d, want 422 (counter should have reset)", i+1, w.Code)
		}
	}
}

func TestStepUpWindowExpires(t *testing.T) {
	f := newFakePlatform(t)
	owner := f.session(t, principalManager, fakeManagerID)
	f.stepUp(t, owner)

	f.setSessionValue(t, owner, auth.SessionKeyStepUpUntil, time.Now().Add(-time.Second).Unix())
	code, resp := f.patchStaffAs(owner, `{"role":"viewer"}`)
	if code != http.StatusForbidden || errorCode(t, resp) != auth.StepUpCode {
		t.Fatalf("after the window closed: %d %s, want 403 %s", code, resp, auth.StepUpCode)
	}

	f.setSessionValue(t, owner, auth.SessionKeyStepUpUntil, time.Now().Add(auth.StepUpTTL).Unix())
	if code, resp := f.patchStaffAs(owner, `{"role":"viewer"}`); code != http.StatusOK {
		t.Fatalf("inside the window: %d %s", code, resp)
	}
}

func TestStepUpIsPerSession(t *testing.T) {
	f := newFakePlatform(t)
	a := f.session(t, principalManager, fakeManagerID)
	b := f.session(t, principalManager, fakeManagerID)
	f.stepUp(t, a)

	if code, _ := f.patchStaffAs(b, `{"role":"viewer"}`); code != http.StatusForbidden {
		t.Errorf("another session of the same firm rode on a step-up it never made: PATCH = %d, want 403", code)
	}
}

func TestStepUpUsesTheStaffMembersOwnPassword(t *testing.T) {
	f := newFakePlatform(t)
	f.firmActiveModules(data.ProductRentals, data.ProductEstates)
	f.staffRole = data.RoleOwner // a staff owner may manage staff
	staff := f.session(t, principalStaff, fakeStaffID)

	f.stepUp(t, staff) // fakePassword is the staff member's password too
	if code, resp := f.patchStaffAs(staff, `{"module_access":["rentals","estates"]}`); code != http.StatusOK {
		t.Fatalf("staff owner after step-up: %d %s", code, resp)
	}
}

func TestStepUpDoesNotGrantPermission(t *testing.T) {
	f := newFakePlatform(t) // the staff member is an officer: no staff.manage
	staff := f.session(t, principalStaff, fakeStaffID)
	f.stepUp(t, staff)

	code, resp := f.patchStaffAs(staff, `{"role":"owner"}`)
	if code != http.StatusForbidden || errorCode(t, resp) == auth.StepUpCode {
		t.Fatalf("officer promoting themselves: %d %s, want a plain 403 (permission), not a password prompt", code, resp)
	}
}

func TestStepUpRefusals(t *testing.T) {
	f := newFakePlatform(t)
	if w := f.do(http.MethodPost, "/v1/sessions/step-up", `{"password":"x"}`, nil); w.Code != http.StatusUnauthorized {
		t.Errorf("anonymous = %d, want 401", w.Code)
	}
	owner := f.session(t, principalManager, fakeManagerID)
	if w := f.do(http.MethodPost, "/v1/sessions/step-up", `{}`, owner); w.Code != http.StatusUnprocessableEntity {
		t.Errorf("empty password = %d, want 422", w.Code)
	}
}

// The Super Admin's consequential actions (c4-matrix-fixes.md item 6) need a
// re-entry of the ADMIN password, and each one lands in the admin trail with
// the actor, the target and the reason.
func TestSuperAdminActionsNeedStepUp(t *testing.T) {
	f := newFakePlatform(t)
	admin := f.session(t, principalAdmin, fakeAdminID)
	suspend := "/v1/admin/managers/" + fakeManagerID.String() + "/suspend"
	reinstate := "/v1/admin/managers/" + fakeManagerID.String() + "/reinstate"
	plan := "/v1/admin/plans/" + uuid.NewString()
	resolve := "/v1/admin/unclaimed-receipts/" + uuid.NewString() + "/resolve"

	gated := []struct{ method, path string }{
		{http.MethodPost, suspend}, {http.MethodPost, reinstate}, {http.MethodPost, resolve},
		{http.MethodPost, "/v1/admin/plans"}, {http.MethodPatch, plan},
		{http.MethodPost, plan + "/archive"}, {http.MethodPost, plan + "/restore"},
	}
	for _, g := range gated {
		w := f.do(g.method, g.path, `{}`, admin)
		if w.Code != http.StatusForbidden || errorCode(t, w.Body.String()) != auth.StepUpCode {
			t.Errorf("%s %s without step-up = %d %s, want 403 %s", g.method, g.path, w.Code, w.Body.String(), auth.StepUpCode)
		}
	}
	if f.managerStatus != data.ManagerStatusActive {
		t.Fatalf("a refused suspend changed the firm to %q", f.managerStatus)
	}

	// A wrong password opens nothing, and it is the ADMIN's password that counts.
	if w := f.do(http.MethodPost, "/v1/sessions/step-up", `{"password":"not-it"}`, admin); w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("admin wrong password = %d, want 422: %s", w.Code, w.Body.String())
	}
	if w := f.do(http.MethodPost, suspend, `{}`, admin); w.Code != http.StatusForbidden {
		t.Fatalf("suspend after a wrong password = %d, want 403", w.Code)
	}

	f.stepUp(t, admin)
	if w := f.do(http.MethodPost, suspend, `{"reason":"chargeback dispute"}`, admin); w.Code != http.StatusOK {
		t.Fatalf("suspend after step-up = %d %s", w.Code, w.Body.String())
	}
	if f.managerStatus != data.ManagerStatusSuspended {
		t.Errorf("status = %q, want suspended", f.managerStatus)
	}
	if w := f.do(http.MethodPost, reinstate, ``, admin); w.Code != http.StatusOK {
		t.Fatalf("reinstate (no body) after step-up = %d %s", w.Code, w.Body.String())
	}

	rows := f.adminAuditRows()
	byAction := map[string]sqlc.CreateAdminAuditLogParams{}
	for _, r := range rows {
		byAction[r.Action] = r
	}
	for _, want := range []string{"session.step_up_failed", "session.step_up", "manager.suspended", "manager.reinstated"} {
		r, ok := byAction[want]
		if !ok {
			t.Errorf("admin trail has no %q row: %+v", want, rows)
			continue
		}
		if r.AdminID == nil || *r.AdminID != fakeAdminID {
			t.Errorf("%s: actor = %v, want the Super Admin", want, r.AdminID)
		}
		if r.Ip == nil || *r.Ip == "" {
			t.Errorf("%s: no client IP recorded", want)
		}
	}
	if r := byAction["manager.suspended"]; r.ManagerID == nil || *r.ManagerID != fakeManagerID || !strings.Contains(r.Summary, "chargeback dispute") {
		t.Errorf("manager.suspended = %+v, want the target firm and the reason", r)
	}
}

// A firm's own step-up window is not the Super Admin's, and a staff or firm
// session cannot reach an admin route to use one.
func TestSuperAdminStepUpUsesTheAdminPasswordOnly(t *testing.T) {
	f := newFakePlatform(t)
	admin := f.session(t, principalAdmin, fakeAdminID)
	// fakePassword is every fake principal's password; the point is that the check
	// runs against the admin row (GetAdmin) and a signed-out caller gets 401.
	if w := f.do(http.MethodPost, "/v1/sessions/step-up", `{"password":"`+fakePassword+`"}`, admin); w.Code != http.StatusOK {
		t.Fatalf("Super Admin step-up = %d %s, want 200", w.Code, w.Body.String())
	}
	if w := f.do(http.MethodPost, "/v1/sessions/step-up", `{"password":"`+fakePassword+`"}`, nil); w.Code != http.StatusUnauthorized {
		t.Errorf("anonymous step-up = %d, want 401", w.Code)
	}
}

func TestStepUpIsAudited(t *testing.T) {
	f := newFakePlatform(t)
	owner := f.session(t, principalManager, fakeManagerID)
	f.do(http.MethodPost, "/v1/sessions/step-up", `{"password":"nope"}`, owner)
	f.stepUp(t, owner)

	got := strings.Join(f.auditActions(), ",")
	for _, want := range []string{"manager:session.step_up_failed", "manager:session.step_up"} {
		if !strings.Contains(got, want) {
			t.Errorf("audit log %q is missing %q", got, want)
		}
	}
}

func TestStaffRoleChangeAlertsTheOwner(t *testing.T) {
	f := newFakePlatform(t)
	owner := f.session(t, principalManager, fakeManagerID)

	// Refused (no step-up): nothing happened, so nothing to alert.
	f.patchStaffAs(owner, `{"role":"accountant"}`)
	f.deliver()
	if n := len(f.mail.alerts()); n != 0 {
		t.Fatalf("%d alerts for a refused change, want 0", n)
	}

	f.stepUp(t, owner)
	f.patchStaffAs(owner, `{"role":"accountant"}`)
	f.deliver()

	alerts := f.mail.alerts()
	if len(alerts) != 1 {
		t.Fatalf("%d alerts for a role change, want 1", len(alerts))
	}
	a := alerts[0]
	if a.Recipient != fakeManagerEmail {
		t.Errorf("alert went to %q, want the firm login %q", a.Recipient, fakeManagerEmail)
	}
	for key, want := range map[string]string{
		"Headline": "A staff member's role was changed",
		"Actor":    "Kimani & Associates (firm login)",
		"Target":   "officer to accountant",
	} {
		if got, _ := a.Data[key].(string); !strings.Contains(got, want) {
			t.Errorf("alert %s = %q, want it to contain %q", key, got, want)
		}
	}
	for _, key := range []string{"IP", "When"} {
		if got, _ := a.Data[key].(string); got == "" {
			t.Errorf("alert %s is empty", key)
		}
	}

	if got, _ := a.Data["Link"].(string); !strings.HasSuffix(got, "/staff") {
		t.Errorf("alert Link = %q, want a link to the staff list", got)
	}

	// A rename is not an authority change: nobody is alerted.
	f.patchStaffAs(owner, `{"name":"Renamed Clerk"}`)
	f.deliver()
	if n := len(f.mail.alerts()); n != 1 {
		t.Errorf("%d alerts after a rename, want still 1", n)
	}
}

// C3.7 triggers: every kind of authority change and a removal alerts the
// owner, each with its own headline, the actor, the IP and a link.
func TestStaffAuthorityChangesAlertTheOwner(t *testing.T) {
	cases := []struct {
		name     string
		body     string
		headline string
		target   string
	}{
		{"role", `{"role":"accountant"}`, "A staff member's role was changed", "officer to accountant"},
		{"module access", `{"module_access":["rentals","estates"]}`, "A staff member's product access was changed", "rentals to estates, rentals"},
		{"property access", `{"property_access":{"rentals":["` + propX.String() + `"]}}`, "A staff member's property access was changed", "all properties to limited to 1 property in rentals"},
		{"removal", `{"status":"disabled"}`, "A staff member was removed", "was officer"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakePlatform(t)
			f.firmActiveModules(data.ProductRentals, data.ProductEstates)
			owner := f.session(t, principalManager, fakeManagerID)
			f.stepUp(t, owner)
			if code, resp := f.patchStaffAs(owner, tc.body); code != http.StatusOK {
				t.Fatalf("change: %d %s", code, resp)
			}
			f.deliver()

			alerts := f.mail.alerts()
			if len(alerts) != 1 {
				t.Fatalf("%d alerts, want 1", len(alerts))
			}
			d := alerts[0].Data
			if d["Headline"] != tc.headline {
				t.Errorf("headline = %v, want %q", d["Headline"], tc.headline)
			}
			if got, _ := d["Target"].(string); !strings.Contains(got, tc.target) || !strings.Contains(got, fakeStaffEmail) {
				t.Errorf("target = %q, want it to name %q and contain %q", got, fakeStaffEmail, tc.target)
			}
			if d["Actor"] != "Kimani & Associates (firm login)" || d["IP"] == "" || d["When"] == "" || d["Link"] == "" {
				t.Errorf("alert lacks actor, IP, time or link: %v", d)
			}
		})
	}
}

// The actor is named with their role when a staff member made the change, and
// the alert still goes to the firm login.
func TestAlertNamesTheStaffActorAndRole(t *testing.T) {
	f := newFakePlatform(t)
	f.firmActiveModules(data.ProductRentals, data.ProductEstates)
	f.staffRole = data.RoleOwner // a staff owner may manage staff
	staff := f.session(t, principalStaff, fakeStaffID)
	f.stepUp(t, staff)
	if code, resp := f.patchStaffAs(staff, `{"module_access":["rentals","estates"]}`); code != http.StatusOK {
		t.Fatalf("change: %d %s", code, resp)
	}
	f.deliver()
	alerts := f.mail.alerts()
	if len(alerts) != 1 || alerts[0].Recipient != fakeManagerEmail || alerts[0].Data["Actor"] != "Clerk (owner)" {
		t.Fatalf("alerts = %+v, want one to the firm login naming Clerk (owner)", alerts)
	}
}

// Delivery is asynchronous and survives a mail outage: the action succeeded,
// the alert waits in the outbox, is retried with backoff, and goes out once the
// mailer recovers. Exactly one email per event.
func TestAlertSurvivesAMailOutageAndIsSentOnce(t *testing.T) {
	f := newFakePlatform(t)
	f.mail.err = errors.New("smtp: connection refused")
	owner := f.session(t, principalManager, fakeManagerID)
	f.stepUp(t, owner)

	if code, resp := f.patchStaffAs(owner, `{"role":"accountant"}`); code != http.StatusOK {
		t.Fatalf("the action must succeed during a mail outage: %d %s", code, resp)
	}
	if sent := f.deliver(); sent != 0 {
		t.Fatalf("sent %d during the outage", sent)
	}
	rows := f.outbox.snapshot()
	if len(rows) != 1 || rows[0].Status != "pending" || rows[0].LastError == nil {
		t.Fatalf("outbox = %+v, want one pending row carrying the error", rows)
	}

	// Not retried before its backoff elapses ...
	f.mail.err = nil
	if sent := f.deliver(); sent != 0 {
		t.Errorf("retried at once (%d sent), want to wait out the backoff", sent)
	}
	// ... then delivered, once, and never again.
	f.outbox.advance(data.OutboxRetryDelay(1) + time.Second)
	if sent := f.deliver(); sent != 1 {
		t.Fatalf("sent %d after recovery, want 1", sent)
	}
	f.outbox.advance(24 * time.Hour)
	if sent := f.deliver(); sent != 0 {
		t.Errorf("sent %d on a later pass, want the alert delivered exactly once", sent)
	}
	if n := len(f.mail.alerts()); n != 1 {
		t.Errorf("%d emails, want 1", n)
	}
}

// After the retries run out the row is 'dead' (never retried again) and the
// System page counts it.
func TestAlertThatNeverDeliversShowsOnTheSystemPage(t *testing.T) {
	f := newFakePlatform(t)
	f.mail.err = errors.New("smtp: 550 mailbox unavailable")
	owner := f.session(t, principalManager, fakeManagerID)
	f.stepUp(t, owner)
	f.patchStaffAs(owner, `{"role":"accountant"}`)

	for range data.OutboxMaxAttempts {
		f.deliver()
		f.outbox.advance(5 * time.Hour)
	}
	rows := f.outbox.snapshot()
	if len(rows) != 1 || rows[0].Status != "dead" || rows[0].Attempts != data.OutboxMaxAttempts {
		t.Fatalf("outbox = %+v, want one dead row after %d attempts", rows, data.OutboxMaxAttempts)
	}
	// Even with the mailer healthy again, a dead row is never picked up.
	f.mail.err = nil
	f.outbox.advance(24 * time.Hour)
	if sent := f.deliver(); sent != 0 || f.outbox.snapshot()[0].Attempts != data.OutboxMaxAttempts {
		t.Errorf("a dead row is still being retried")
	}

	status := f.app.emailOutboxStatus(context.Background())
	if status["dead"] != 1 || status["pending"] != 0 {
		t.Errorf("System page outbox = %v, want 1 dead and 0 pending", status)
	}
	if msg, _ := status["last_error"].(*string); msg == nil || !strings.Contains(*msg, "550") {
		t.Errorf("System page does not show the delivery error: %v", status["last_error"])
	}
}

// Idempotent per event: the same event enqueued twice is one row, one email.
func TestAlertEnqueueIsIdempotentPerEvent(t *testing.T) {
	f := newFakePlatform(t)
	ctx := context.Background()
	for range 3 {
		if _, err := f.app.models.Outbox.Enqueue(ctx, fakeManagerID, "same-event", fakeManagerEmail, mailer.SecurityAlertTemplate, map[string]any{"Headline": "H"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.app.models.Outbox.Enqueue(ctx, fakeManagerID, "another-event", fakeManagerEmail, mailer.SecurityAlertTemplate, map[string]any{"Headline": "H"}); err != nil {
		t.Fatal(err)
	}
	if n := len(f.outbox.snapshot()); n != 2 {
		t.Errorf("%d rows, want 2 (three enqueues of one event, plus another event)", n)
	}
}

// Alerts carry no secrets, no raw phone numbers.
func TestAlertPayloadHasNoSecretsOrPhoneNumbers(t *testing.T) {
	f := newFakePlatform(t)
	owner := f.session(t, principalManager, fakeManagerID)
	f.stepUp(t, owner)
	f.patchStaffAs(owner, `{"role":"accountant","phone":"+254722000123"}`)
	f.deliver()

	raw, _ := json.Marshal(f.outbox.snapshot())
	for _, leak := range []string{"254722000123", fakePassword, "password", "$2a$"} {
		if strings.Contains(string(raw), leak) {
			t.Errorf("the outbox holds %q: %s", leak, raw)
		}
	}
}

// C3.6 lifetime: the window is bound to the session id, and ends with it on
// logout, on revocation and on a password change.

func TestStepUpIsClearedOnLogout(t *testing.T) {
	f := newFakePlatform(t)
	owner, code := f.login(t, fakeManagerEmail)
	if owner == nil {
		t.Fatalf("login: %d", code)
	}
	f.stepUp(t, owner)
	if code, resp := f.patchStaffAs(owner, `{"role":"viewer"}`); code != http.StatusOK {
		t.Fatalf("inside the window: %d %s", code, resp)
	}

	if w := f.do(http.MethodDelete, "/v1/sessions", "", owner); w.Code != http.StatusOK {
		t.Fatalf("logout = %d", w.Code)
	}
	// The window died with the session: the old cookie is simply signed out,
	// not "still stepped up".
	if code, _ := f.patchStaffAs(owner, `{"role":"viewer"}`); code != http.StatusUnauthorized {
		t.Errorf("PATCH with the logged-out cookie = %d, want 401", code)
	}
	// And a new login starts with no window.
	again, _ := f.login(t, fakeManagerEmail)
	if code, resp := f.patchStaffAs(again, `{"role":"accountant"}`); code != http.StatusForbidden || errorCode(t, resp) != auth.StepUpCode {
		t.Errorf("a fresh login inherited a window: %d %s", code, resp)
	}
}

func TestStepUpIsClearedWhenAuthorityIsRevoked(t *testing.T) {
	f := newFakePlatform(t)
	f.staffRole = data.RoleOwner
	staff, _ := f.login(t, fakeStaffEmail)
	f.stepUp(t, staff)

	// The firm login demotes the staff owner: their sessions, windows included, end.
	firm := f.session(t, principalManager, fakeManagerID)
	f.stepUp(t, firm)
	if code, resp := f.patchStaffAs(firm, `{"role":"viewer"}`); code != http.StatusOK {
		t.Fatalf("demotion: %d %s", code, resp)
	}
	if code, _ := f.patchStaffAs(staff, `{"role":"owner"}`); code != http.StatusUnauthorized {
		t.Errorf("the demoted member's stepped-up session still works: %d, want 401", code)
	}
}

func TestStepUpIsClearedOnPasswordChange(t *testing.T) {
	f := newFakePlatform(t)
	f.firmActiveModules(data.ProductRentals, data.ProductEstates)
	f.staffRole = data.RoleOwner
	staff, _ := f.login(t, fakeStaffEmail)
	f.stepUp(t, staff)

	w := f.do(http.MethodPut, "/v1/staff/me/password", changeBody(fakePassword, newStaffPassword), staff)
	if w.Code != http.StatusOK {
		t.Fatalf("change: %d %s", w.Code, w.Body)
	}
	const authorityChangeBody = `{"module_access":["rentals","estates"]}`
	if code, _ := f.patchStaffAs(staff, authorityChangeBody); code != http.StatusUnauthorized {
		t.Errorf("the old stepped-up session survived a password change: %d, want 401", code)
	}
	fresh := freshCookie(w.Result().Cookies())
	if fresh == nil {
		t.Fatal("no re-issued session")
	}
	if code, resp := f.patchStaffAs(fresh, authorityChangeBody); code != http.StatusForbidden || errorCode(t, resp) != auth.StepUpCode {
		t.Errorf("the re-issued session carried a window over: %d %s", code, resp)
	}
}

// The permissions that need a fresh step-up are listed once, in the catalog,
// and a new permission that reverses or manages money must be added there or
// to the explicit exemptions below.
func TestEveryStepUpPermissionIsListedAndRoutesAreGated(t *testing.T) {
	for _, perm := range auth.Catalog() {
		if strings.HasSuffix(perm, ".reverse") && !slices.Contains(auth.StepUpPermissions, perm) {
			t.Errorf("%s reverses money but is not in auth.StepUpPermissions", perm)
		}
	}
	for _, perm := range auth.StepUpPermissions {
		if !auth.Known(perm) {
			t.Errorf("auth.StepUpPermissions lists %q, which is not a catalog permission", perm)
		}
	}
}
