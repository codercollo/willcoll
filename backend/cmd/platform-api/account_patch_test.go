package main

import (
	"net/http"
	"strings"
	"testing"

	"github.com/codercollo/willcoll/backend/internal/platform/data"
)

// PATCH /v1/account edits the firm's profile, so only the owner may call it
// (system-design.txt A.1): the firm login or an owner-role staff member.

const patchAccountBody = `{"firm_name":"Renamed Co","phone":"+254711111111"}`

func TestUpdateAccountIsOwnerOnly(t *testing.T) {
	f := newFakePlatform(t)

	for _, role := range []string{data.RoleAccountant, data.RoleOfficer, data.RoleViewer} {
		t.Run(role, func(t *testing.T) {
			f.mu.Lock()
			f.staffRole = role
			f.mu.Unlock()
			staff := f.session(t, principalStaff, fakeStaffID)

			w := f.do(http.MethodPatch, "/v1/account", patchAccountBody, staff)
			if w.Code != http.StatusForbidden {
				t.Fatalf("%s: %d %s, want 403", role, w.Code, w.Body)
			}
			// Same error shape as the firm-password route's refusal for staff.
			other := f.do(http.MethodPut, "/v1/account/password", changeBody(fakePassword, newStaffPassword), staff)
			if w.Body.String() != other.Body.String() {
				t.Errorf("403 body %q differs from PUT /v1/account/password's %q", w.Body, other.Body)
			}
		})
	}
	if f.firmName != "Kimani & Associates" || len(f.auditActions()) != 0 {
		t.Errorf("a refused request changed the firm (%q) or wrote audit rows: %v", f.firmName, f.auditActions())
	}
}

func TestUpdateAccountOwner(t *testing.T) {
	for name, principal := range map[string]string{"the firm login": principalManager, "an owner-role staff member": principalStaff} {
		t.Run(name, func(t *testing.T) {
			f := newFakePlatform(t)
			id := fakeManagerID
			if principal == principalStaff {
				f.staffRole = data.RoleOwner
				id = fakeStaffID
			}

			w := f.do(http.MethodPatch, "/v1/account", patchAccountBody, f.session(t, principal, id))
			if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Renamed Co") {
				t.Fatalf("%d %s, want 200 with the new firm name", w.Code, w.Body)
			}
			if f.firmName != "Renamed Co" {
				t.Errorf("stored firm name %q", f.firmName)
			}
			actions := f.auditActions()
			if len(actions) != 1 || !strings.HasSuffix(actions[0], ":account.updated") {
				t.Errorf("audit rows %v, want one account.updated", actions)
			}
		})
	}
}

func TestUpdateAccountRequiresSignIn(t *testing.T) {
	f := newFakePlatform(t)
	if w := f.do(http.MethodPatch, "/v1/account", patchAccountBody, nil); w.Code != http.StatusUnauthorized {
		t.Errorf("anonymous: %d, want 401", w.Code)
	}
}
