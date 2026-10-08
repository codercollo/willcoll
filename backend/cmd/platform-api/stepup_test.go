package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/codercollo/willcoll/backend/internal/platform/data"
	"github.com/codercollo/willcoll/backend/internal/shared/auth"
	"github.com/codercollo/willcoll/backend/internal/shared/mailer"
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
	f.staffRole = data.RoleOwner // a staff owner may manage staff
	staff := f.session(t, principalStaff, fakeStaffID)

	f.stepUp(t, staff) // fakePassword is the staff member's password too
	if code, resp := f.patchStaffAs(staff, `{"role":"viewer"}`); code != http.StatusOK {
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
	admin := f.session(t, principalAdmin, fakeAdminID)
	if w := f.do(http.MethodPost, "/v1/sessions/step-up", `{"password":"x"}`, admin); w.Code != http.StatusForbidden {
		t.Errorf("Super Admin = %d, want 403", w.Code)
	}
	owner := f.session(t, principalManager, fakeManagerID)
	if w := f.do(http.MethodPost, "/v1/sessions/step-up", `{}`, owner); w.Code != http.StatusUnprocessableEntity {
		t.Errorf("empty password = %d, want 422", w.Code)
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
	f.app.wg.Wait()
	if n := len(f.mail.alerts()); n != 0 {
		t.Fatalf("%d alerts for a refused change, want 0", n)
	}

	f.stepUp(t, owner)
	f.patchStaffAs(owner, `{"role":"accountant"}`)
	f.app.wg.Wait()

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

	// A change that is not a role change (module scope) alerts nobody.
	f.patchStaffAs(owner, `{"module_access":["rentals","estates"]}`)
	f.app.wg.Wait()
	if n := len(f.mail.alerts()); n != 1 {
		t.Errorf("%d alerts after a module change, want still 1", n)
	}
}
