package main

import (
	"net/http"
	"strings"
	"testing"
)

// A staff member changes their OWN password (build-phases.txt 3l follow-up):
// PUT /v1/staff/me/password. Same fake platform as admin_managers_test.go.

const newStaffPassword = "a-brand-new-passphrase"

func changeBody(current, next string) string {
	return `{"current_password":"` + current + `","new_password":"` + next + `"}`
}

// freshCookie is the session cookie a response set, or nil.
func freshCookie(resp []*http.Cookie) *http.Cookie {
	for _, c := range resp {
		if c.Name == fakeSessionCookie && c.Value != "" {
			return c
		}
	}
	return nil
}

func TestStaffChangeOwnPassword(t *testing.T) {
	f := newFakePlatform(t)
	laptop, code := f.login(t, fakeStaffEmail)
	if laptop == nil {
		t.Fatalf("staff login: %d", code)
	}
	phone, _ := f.login(t, fakeStaffEmail)

	w := f.do(http.MethodPut, "/v1/staff/me/password", changeBody(fakePassword, newStaffPassword), laptop)
	if w.Code != http.StatusOK {
		t.Fatalf("change: %d %s", w.Code, w.Body)
	}
	fresh := freshCookie(w.Result().Cookies())
	if fresh == nil {
		t.Fatal("the session that changed the password must be re-issued, not left to die")
	}

	// This device stays signed in (as staff, not as the firm) ...
	if w := f.do(http.MethodGet, "/v1/sessions", "", fresh); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"principal_type": "staff"`) {
		t.Errorf("fresh session: %d %s", w.Code, w.Body)
	}
	// ... every other session of theirs is signed out, including the old token of this one.
	for name, c := range map[string]*http.Cookie{"phone": phone, "old laptop token": laptop} {
		if w := f.do(http.MethodGet, "/v1/sessions", "", c); w.Code != http.StatusUnauthorized {
			t.Errorf("%s still signed in after the password change: %d", name, w.Code)
		}
	}

	// The old password is dead, the new one signs in.
	if w := f.do(http.MethodPost, "/v1/sessions", `{"email":"`+fakeStaffEmail+`","password":"`+fakePassword+`"}`, nil); w.Code != http.StatusUnauthorized {
		t.Errorf("login with the old password: %d, want 401", w.Code)
	}
	if w := f.do(http.MethodPost, "/v1/sessions", `{"email":"`+fakeStaffEmail+`","password":"`+newStaffPassword+`"}`, nil); w.Code != http.StatusCreated && w.Code != http.StatusOK {
		t.Errorf("login with the new password: %d %s", w.Code, w.Body)
	}

	found := false
	for _, a := range f.auditActions() {
		if a == "staff:account.password_changed" {
			found = true
		}
	}
	if !found {
		t.Errorf("no staff audit row for the change: %v", f.auditActions())
	}
}

func TestStaffChangeOwnPasswordRefusals(t *testing.T) {
	f := newFakePlatform(t)
	staff := f.session(t, principalStaff, fakeStaffID)
	hashBefore := string(f.staffHash)

	cases := []struct {
		name, body, field string
	}{
		{"wrong current password", changeBody("not-my-password", newStaffPassword), "current_password"},
		{"empty current password", changeBody("", newStaffPassword), "current_password"},
		{"too short", changeBody(fakePassword, "short"), "new_password"},
		{"same as current", changeBody(fakePassword, fakePassword), "new_password"},
		{"their email", changeBody(fakePassword, fakeStaffEmail), "new_password"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := f.do(http.MethodPut, "/v1/staff/me/password", tc.body, staff)
			if w.Code != http.StatusUnprocessableEntity || !strings.Contains(w.Body.String(), tc.field) {
				t.Errorf("%d %s, want 422 naming %s", w.Code, w.Body, tc.field)
			}
		})
	}
	if w := f.do(http.MethodPut, "/v1/staff/me/password", `{"current_password":`, staff); w.Code != http.StatusBadRequest {
		t.Errorf("malformed body: %d, want 400", w.Code)
	}

	if string(f.staffHash) != hashBefore {
		t.Error("a refused request changed the stored password")
	}
	if got := f.auditActions(); len(got) != 0 {
		t.Errorf("refused requests wrote audit rows: %v", got)
	}
	// The session survived every refusal.
	if w := f.do(http.MethodGet, "/v1/sessions", "", staff); w.Code != http.StatusOK {
		t.Errorf("session after refusals: %d", w.Code)
	}
}

func TestStaffPasswordRouteIsOnlyForStaff(t *testing.T) {
	f := newFakePlatform(t)
	body := changeBody(fakePassword, newStaffPassword)

	if w := f.do(http.MethodPut, "/v1/staff/me/password", body, nil); w.Code != http.StatusUnauthorized {
		t.Errorf("anonymous: %d, want 401", w.Code)
	}
	for name, c := range map[string]*http.Cookie{
		"the firm login": f.session(t, principalManager, fakeManagerID),
		"a Super Admin":  f.session(t, principalAdmin, fakeAdminID),
	} {
		if w := f.do(http.MethodPut, "/v1/staff/me/password", body, c); w.Code != http.StatusForbidden && w.Code != http.StatusUnauthorized {
			t.Errorf("%s: %d, want 401/403", name, w.Code)
		}
	}
	if string(f.staffHash) == "" || f.staffVersion != 1 {
		t.Errorf("a refused request touched the staff row (version %d)", f.staffVersion)
	}
}

// The firm login's route must not be usable by a staff session: it would check
// the FIRM password and rewrite it.
func TestStaffCannotChangeTheFirmPassword(t *testing.T) {
	f := newFakePlatform(t)
	staff := f.session(t, principalStaff, fakeStaffID)
	w := f.do(http.MethodPut, "/v1/account/password", changeBody(fakePassword, newStaffPassword), staff)
	if w.Code != http.StatusForbidden {
		t.Errorf("staff on the firm password route: %d %s, want 403", w.Code, w.Body)
	}
	if got := f.auditActions(); len(got) != 0 {
		t.Errorf("refused request wrote audit rows: %v", got)
	}
}
