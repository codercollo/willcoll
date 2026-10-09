package main

import (
	"net/http"
	"strings"
	"testing"

	"github.com/codercollo/willcoll/backend/internal/platform/data"
)

// C3.8: M8 (module_access can only name products the firm holds) and D3 (only
// the firm login grants or revokes the owner role).

const inviteBody = `{"name":"New Hire","email":"new@firm.example.com","role":"officer","module_access":["%s"]}`

func inviteWith(module string) string { return strings.Replace(inviteBody, "%s", module, 1) }

func TestInviteRejectsAModuleTheFirmDoesNotHold(t *testing.T) {
	f := newFakePlatform(t) // the firm holds rentals only
	owner := f.session(t, principalManager, fakeManagerID)
	f.stepUp(t, owner)

	w := f.do(http.MethodPost, "/v1/staff", inviteWith("estates"), owner)
	if w.Code != http.StatusUnprocessableEntity || !strings.Contains(w.Body.String(), "module_access") {
		t.Fatalf("invite for estates = %d %s, want 422 naming module_access", w.Code, w.Body)
	}
	// (The positive case, a held module, is the PATCH test below: the fake
	// store has no staff-creation path.)
}

func TestInviteRejectsASuspendedOrRequestedModule(t *testing.T) {
	for _, status := range []string{data.ModuleStatusSuspended, data.ModuleStatusRequested} {
		f := newFakePlatform(t)
		f.modules = map[string]string{data.ProductRentals: data.ModuleStatusActive, data.ProductEstates: status}
		owner := f.session(t, principalManager, fakeManagerID)
		f.stepUp(t, owner)
		if w := f.do(http.MethodPost, "/v1/staff", inviteWith("estates"), owner); w.Code != http.StatusUnprocessableEntity {
			t.Errorf("a %s module: invite = %d, want 422", status, w.Code)
		}
	}
}

func TestUpdateRejectsGainingAModuleTheFirmDoesNotHold(t *testing.T) {
	f := newFakePlatform(t)
	owner := f.session(t, principalManager, fakeManagerID)
	f.stepUp(t, owner)

	code, resp := f.patchStaffAs(owner, `{"module_access":["rentals","waste"]}`)
	if code != http.StatusUnprocessableEntity || !strings.Contains(resp, "waste") {
		t.Fatalf("granting waste = %d %s, want 422 naming it", code, resp)
	}
	if f.staffModules != nil {
		t.Errorf("a refused change was saved: %v", f.staffModules)
	}
	f.firmActiveModules(data.ProductRentals, data.ProductWaste)
	if code, resp := f.patchStaffAs(owner, `{"module_access":["rentals","waste"]}`); code != http.StatusOK {
		t.Fatalf("granting waste once the firm holds it = %d %s", code, resp)
	}
}

// A product the member already has is not re-checked: an edit that merely
// repeats it must not fail because the firm's module has since lapsed.
func TestUpdateKeepingAnExistingModuleIsNotRecheckedAgainstTheFirm(t *testing.T) {
	f := newFakePlatform(t)
	f.staffModules = []string{"rentals", "estates"} // granted when the firm held estates
	owner := f.session(t, principalManager, fakeManagerID)
	f.stepUp(t, owner)
	if code, resp := f.patchStaffAs(owner, `{"module_access":["rentals","estates"],"name":"Renamed"}`); code != http.StatusOK {
		t.Fatalf("unchanged modules on a firm that no longer holds estates = %d %s, want 200", code, resp)
	}
}

// D3: a staff owner manages staff but may not grant or revoke owner.
func TestOnlyTheFirmLoginGrantsOrRevokesOwner(t *testing.T) {
	t.Run("a staff owner cannot invite an owner", func(t *testing.T) {
		f := newFakePlatform(t)
		f.staffRole = data.RoleOwner
		staff := f.session(t, principalStaff, fakeStaffID)
		f.stepUp(t, staff)
		w := f.do(http.MethodPost, "/v1/staff", `{"name":"X","email":"x@firm.example.com","role":"owner","module_access":["rentals"]}`, staff)
		if w.Code != http.StatusForbidden || errorCode(t, w.Body.String()) != "" {
			t.Fatalf("staff owner inviting an owner = %d %s, want a plain 403", w.Code, w.Body)
		}
	})

	// The fake has one staff row, so the acting staff owner is also the target:
	// demoting oneself from owner is a revocation of owner, which only the firm
	// login may do (granting is covered by the invite case above, same rule).
	t.Run("a staff owner cannot revoke owner", func(t *testing.T) {
		f := newFakePlatform(t)
		f.staffRole = data.RoleOwner
		owner := f.session(t, principalStaff, fakeStaffID)
		f.stepUp(t, owner)
		code, resp := f.patchStaffAs(owner, `{"role":"viewer"}`)
		if code != http.StatusForbidden || errorCode(t, resp) != "" {
			t.Fatalf("a staff owner revoking owner = %d %s, want a plain 403", code, resp)
		}
		if f.staffRole != data.RoleOwner {
			t.Errorf("the refused change was saved: role %q", f.staffRole)
		}
		// A change that is not about owner is still theirs to make.
		if code, resp := f.patchStaffAs(owner, `{"name":"Renamed"}`); code != http.StatusOK {
			t.Errorf("a rename by a staff owner = %d %s, want 200", code, resp)
		}
	})

	t.Run("the firm login can grant and revoke owner", func(t *testing.T) {
		f := newFakePlatform(t)
		firm := f.session(t, principalManager, fakeManagerID)
		f.stepUp(t, firm)
		if code, resp := f.patchStaffAs(firm, `{"role":"owner"}`); code != http.StatusOK {
			t.Fatalf("firm login granting owner = %d %s", code, resp)
		}
		if code, resp := f.patchStaffAs(firm, `{"role":"accountant"}`); code != http.StatusOK {
			t.Fatalf("firm login revoking owner = %d %s", code, resp)
		}
	})

	t.Run("the permission check comes before the password prompt", func(t *testing.T) {
		f := newFakePlatform(t)
		f.staffRole = data.RoleOwner
		staff := f.session(t, principalStaff, fakeStaffID) // no step-up
		code, resp := f.patchStaffAs(staff, `{"role":"viewer"}`)
		if code != http.StatusForbidden || errorCode(t, resp) == "step_up_required" {
			t.Fatalf("= %d %s, want a plain 403 rather than a password prompt that could never help", code, resp)
		}
	})
}
