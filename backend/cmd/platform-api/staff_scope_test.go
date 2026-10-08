package main

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/codercollo/willcoll/backend/internal/platform/data"
	"github.com/google/uuid"
)

// Staff authority changes end the staff member's sessions (authorization.md
// M20, C2): demotion, removal (disabling), a module change and a property
// scope change each sign them out everywhere; an edit that changes no
// authority does not.

var (
	propX = uuid.MustParse("00000000-0000-0000-0000-00000000a001")
	propY = uuid.MustParse("00000000-0000-0000-0000-00000000a002")
)

func staffSessionAlive(t *testing.T, f *fakePlatform, c *http.Cookie) bool {
	t.Helper()
	switch w := f.do(http.MethodGet, "/v1/account", "", c); w.Code {
	case http.StatusOK:
		return true
	case http.StatusUnauthorized:
		return false
	default:
		t.Fatalf("GET /v1/account as staff = %d: %s", w.Code, w.Body.String())
		return false
	}
}

func patchStaff(t *testing.T, f *fakePlatform, body string) (int, string) {
	t.Helper()
	owner := f.session(t, principalManager, fakeManagerID)
	w := f.do(http.MethodPatch, "/v1/staff/"+fakeStaffID.String(), body, owner)
	return w.Code, w.Body.String()
}

func TestStaffAuthorityChangeRevokesSessions(t *testing.T) {
	cases := []struct {
		name       string
		setup      func(f *fakePlatform)
		body       string
		wantRevoke bool
	}{
		{"demotion", func(f *fakePlatform) { f.staffRole = data.RoleAccountant }, `{"role":"viewer"}`, true},
		{"promotion", func(f *fakePlatform) { f.staffRole = data.RoleViewer }, `{"role":"accountant"}`, true},
		{"removal (disable)", nil, `{"status":"disabled"}`, true},
		{"module access change", nil, `{"module_access":["rentals","estates"]}`, true},
		{"property scope added", nil, `{"property_access":{"rentals":["` + propX.String() + `"]}}`, true},
		{"property scope widened to all", func(f *fakePlatform) {
			f.staffProps = []byte(`{"rentals":["` + propX.String() + `"]}`)
		}, `{"property_access":{}}`, true},
		{"property scope changed", func(f *fakePlatform) {
			f.staffProps = []byte(`{"rentals":["` + propX.String() + `"]}`)
		}, `{"property_access":{"rentals":["` + propY.String() + `"]}}`, true},

		// No change in authority: the member stays signed in.
		{"rename only", nil, `{"name":"Renamed Clerk"}`, false},
		{"same role", nil, `{"role":"officer"}`, false},
		{"same scope, reordered", func(f *fakePlatform) {
			f.staffProps = []byte(`{"rentals":["` + propX.String() + `","` + propY.String() + `"]}`)
		}, `{"property_access":{"rentals":["` + propY.String() + `","` + propX.String() + `"]}}`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakePlatform(t)
			if tc.setup != nil {
				tc.setup(f)
			}
			staff := f.session(t, principalStaff, fakeStaffID)
			if !staffSessionAlive(t, f, staff) {
				t.Fatal("staff session not valid before the change")
			}
			if code, body := patchStaff(t, f, tc.body); code != http.StatusOK {
				t.Fatalf("PATCH = %d: %s", code, body)
			}
			if alive := staffSessionAlive(t, f, staff); alive == tc.wantRevoke {
				t.Errorf("session alive after the change = %v, want %v", alive, !tc.wantRevoke)
			}
		})
	}
}

func TestStaffSessionsOfOtherMembersSurviveARevocation(t *testing.T) {
	f := newFakePlatform(t)
	ownerCookie := f.session(t, principalManager, fakeManagerID)
	staff := f.session(t, principalStaff, fakeStaffID)
	if code, body := patchStaff(t, f, `{"role":"viewer"}`); code != http.StatusOK {
		t.Fatalf("PATCH = %d: %s", code, body)
	}
	if w := f.do(http.MethodGet, "/v1/account", "", ownerCookie); w.Code != http.StatusOK {
		t.Errorf("the firm login was signed out by a staff change: %d", w.Code)
	}
	if staffSessionAlive(t, f, staff) {
		t.Error("the demoted member is still signed in")
	}
}

func TestStaffPropertyAccessValidationAndPersistence(t *testing.T) {
	f := newFakePlatform(t)

	t.Run("saved and returned", func(t *testing.T) {
		code, body := patchStaff(t, f, `{"property_access":{"rentals":["`+propX.String()+`","`+propX.String()+`"]}}`)
		if code != http.StatusOK {
			t.Fatalf("PATCH = %d: %s", code, body)
		}
		var out struct {
			Staff struct {
				PropertyAccess map[string][]uuid.UUID `json:"property_access"`
			} `json:"staff"`
		}
		if err := json.Unmarshal([]byte(body), &out); err != nil {
			t.Fatal(err)
		}
		if got := out.Staff.PropertyAccess["rentals"]; len(got) != 1 || got[0] != propX {
			t.Errorf("property_access = %v, want [propX] (duplicates dropped)", out.Staff.PropertyAccess)
		}
	})

	t.Run("empty list means all", func(t *testing.T) {
		code, body := patchStaff(t, f, `{"property_access":{"rentals":[]}}`)
		if code != http.StatusOK {
			t.Fatalf("PATCH = %d: %s", code, body)
		}
		var out struct {
			Staff struct {
				PropertyAccess map[string][]uuid.UUID `json:"property_access"`
			} `json:"staff"`
		}
		_ = json.Unmarshal([]byte(body), &out)
		if len(out.Staff.PropertyAccess) != 0 {
			t.Errorf("empty list stored as %v, want no restriction", out.Staff.PropertyAccess)
		}
	})

	for name, tc := range map[string]struct {
		body string
		want int
	}{
		"owners cannot be restricted":         {`{"role":"owner","property_access":{"rentals":["` + propX.String() + `"]}}`, http.StatusUnprocessableEntity},
		"unknown product":                     {`{"property_access":{"hotels":["` + propX.String() + `"]}}`, http.StatusUnprocessableEntity},
		"product the member has no access to": {`{"property_access":{"estates":["` + propX.String() + `"]}}`, http.StatusUnprocessableEntity},
		"not a uuid":                          {`{"property_access":{"rentals":["not-a-uuid"]}}`, http.StatusBadRequest},
		"wrong shape":                         {`{"property_access":["` + propX.String() + `"]}`, http.StatusBadRequest},
	} {
		t.Run(name, func(t *testing.T) {
			if code, body := patchStaff(t, f, tc.body); code != tc.want {
				t.Errorf("PATCH = %d, want %d: %s", code, tc.want, body)
			}
		})
	}

	t.Run("too many properties", func(t *testing.T) {
		ids := ""
		for i := 0; i < 201; i++ {
			if i > 0 {
				ids += ","
			}
			ids += `"` + uuid.NewString() + `"`
		}
		if code, _ := patchStaff(t, f, `{"property_access":{"rentals":[`+ids+`]}}`); code != http.StatusUnprocessableEntity {
			t.Errorf("201 properties = %d, want 422", code)
		}
	})
}

func TestStaffPropertyScopeHelper(t *testing.T) {
	scoped := &data.Staff{Role: data.RoleAccountant, PropertyAccess: map[string][]uuid.UUID{"rentals": {propX}}}
	if got := scoped.PropertyScope("rentals"); len(got) != 1 || got[0] != propX {
		t.Errorf("scope = %v", got)
	}
	if got := scoped.PropertyScope("estates"); got != nil {
		t.Errorf("a product with no list must be unrestricted, got %v", got)
	}
	owner := &data.Staff{Role: data.RoleOwner, PropertyAccess: map[string][]uuid.UUID{"rentals": {propX}}}
	if got := owner.PropertyScope("rentals"); got != nil {
		t.Errorf("an owner is never restricted, got %v", got)
	}
	if got := data.AnonymousStaff.PropertyScope("rentals"); got != nil {
		t.Errorf("the firm login is never restricted, got %v", got)
	}
}
