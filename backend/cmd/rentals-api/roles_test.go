package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	platformdata "github.com/codercollo/willcoll/backend/internal/platform/data"
	platformsqlc "github.com/codercollo/willcoll/backend/internal/platform/db/sqlc"
	"github.com/google/uuid"
	"go.uber.org/mock/gomock"
)

// build-phases.txt 3f-2: requireRole/requireStaffModuleAccess, and the
// tryStaffSession path in authenticate() that feeds them. Extends
// middleware_test.go's newMiddlewareTestApp with a platform mock store and
// a second (platform-reading) session manager, so a staff login can be
// simulated the way platform-api's own login would leave it: a session row
// in willcoll_platform, presented as the "willcoll_session" cookie.

type roleTestApp struct {
	*middlewareTestApp
}

func newRoleTestApp(t *testing.T) *roleTestApp {
	t.Helper()
	return &roleTestApp{newMiddlewareTestApp(t)}
}

// staffCookie simulates a staff login already performed against
// platform-api: a session in the platform session store, presented as the
// "willcoll_session" cookie a browser would send.
func (m *roleTestApp) staffCookie(t *testing.T, staffID uuid.UUID) *http.Cookie {
	t.Helper()
	return m.sessionCookie(t, map[string]string{sessionKeyPrincipalType: principalStaff, sessionKeyPrincipalID: staffID.String()})
}

func (m *roleTestApp) do(method, target string, cookie *http.Cookie) *httptest.ResponseRecorder {
	return m.middlewareTestApp.do(method, target, cookie, nil)
}

func testStaffRow(role string, moduleAccess ...string) platformsqlc.Staff {
	return platformsqlc.Staff{
		ID: uuid.New(), ManagerID: testManagerID, Name: "Staffer", Email: "staff@example.com",
		Role: role, ModuleAccess: moduleAccess, Status: platformdata.StaffActive, Version: 1,
	}
}

func TestMinRoleFor(t *testing.T) {
	tests := []struct {
		method, path string
		want         string
	}{
		{"GET", "/v1/properties", platformdata.RoleViewer},      // unlisted GET defaults to viewer
		{"POST", "/v1/properties", platformdata.RoleAccountant}, // explicitly listed
		{"POST", "/v1/units/:id/rent/payments", platformdata.RoleOfficer},
		{"POST", "/v1/ledger-entries/:id/reverse", platformdata.RoleAccountant},
		{"POST", "/v1/some/unlisted/write", platformdata.RoleOwner}, // unlisted write defaults to owner
	}
	for _, tc := range tests {
		if got := minRoleFor(tc.method, tc.path); got != tc.want {
			t.Errorf("minRoleFor(%q, %q) = %q, want %q", tc.method, tc.path, got, tc.want)
		}
	}
}

func TestRequireRole_StaffOfficer(t *testing.T) {
	m := newRoleTestApp(t)
	staff := testStaffRow(platformdata.RoleOfficer, "rentals")
	m.pStore.EXPECT().GetStaff(gomock.Any(), staff.ID).Return(staff, nil).AnyTimes()
	m.pStore.EXPECT().GetManager(gomock.Any(), testManagerID).Return(managerRow(platformdata.ManagerStatusActive), nil).AnyTimes()
	cookie := m.staffCookie(t, staff.ID)

	t.Run("allowed: cash receipts (invalid id 404s past every gate)", func(t *testing.T) {
		w := m.do(http.MethodPost, "/v1/units/not-a-uuid/rent/payments", cookie)
		if w.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404 (past the gates, into the handler)", w.Code)
		}
	})

	t.Run("forbidden: a reversal needs accountant", func(t *testing.T) {
		w := m.do(http.MethodPost, "/v1/ledger-entries/not-a-uuid/reverse", cookie)
		if w.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403", w.Code)
		}
	})
}

func TestRequireRole_StaffViewer(t *testing.T) {
	m := newRoleTestApp(t)
	staff := testStaffRow(platformdata.RoleViewer, "rentals")
	m.pStore.EXPECT().GetStaff(gomock.Any(), staff.ID).Return(staff, nil).AnyTimes()
	m.pStore.EXPECT().GetManager(gomock.Any(), testManagerID).Return(managerRow(platformdata.ManagerStatusActive), nil).AnyTimes()
	cookie := m.staffCookie(t, staff.ID)

	if w := m.do(http.MethodPost, "/v1/properties", cookie); w.Code != http.StatusForbidden {
		t.Errorf("viewer POST /v1/properties: status = %d, want 403", w.Code)
	}
	// A viewer's GET must still pass every gate — 404 (bad id, into the
	// handler) proves that without needing to mock a full property lookup.
	if w := m.do(http.MethodGet, "/v1/properties/not-a-uuid", cookie); w.Code != http.StatusNotFound {
		t.Errorf("viewer GET: status = %d, want 404 (past the gates)", w.Code)
	}
}

func TestRequireStaffModuleAccess(t *testing.T) {
	m := newRoleTestApp(t)
	staff := testStaffRow(platformdata.RoleOwner, "estates") // no "rentals"
	m.pStore.EXPECT().GetStaff(gomock.Any(), staff.ID).Return(staff, nil).AnyTimes()
	m.pStore.EXPECT().GetManager(gomock.Any(), testManagerID).Return(managerRow(platformdata.ManagerStatusActive), nil).AnyTimes()
	cookie := m.staffCookie(t, staff.ID)

	if w := m.do(http.MethodGet, "/v1/properties/not-a-uuid", cookie); w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403 (no rentals module_access, regardless of role)", w.Code)
	}
}

func TestTryStaffSession_Disabled(t *testing.T) {
	m := newRoleTestApp(t)
	staff := testStaffRow(platformdata.RoleOwner, "rentals")
	staff.Status = platformdata.StaffDisabled
	m.pStore.EXPECT().GetStaff(gomock.Any(), staff.ID).Return(staff, nil)
	cookie := m.staffCookie(t, staff.ID)

	if w := m.do(http.MethodGet, "/v1/properties", cookie); w.Code != http.StatusUnauthorized {
		t.Errorf("disabled staff: status = %d, want 401", w.Code)
	}
}

func TestTryStaffSession_PlatformReadError(t *testing.T) {
	m := newRoleTestApp(t)
	staffID := uuid.New()
	m.pStore.EXPECT().GetStaff(gomock.Any(), staffID).Return(platformsqlc.Staff{}, errors.New("connection reset"))
	cookie := m.staffCookie(t, staffID)

	if w := m.do(http.MethodGet, "/v1/properties", cookie); w.Code != http.StatusServiceUnavailable {
		t.Errorf("platform read error: status = %d, want 503", w.Code)
	}
}

// A manager-principal session needs no staff lookup at all: only the
// manager row is read, never willcoll_platform's staff table.
func TestManagerSessionNeverReadsStaff(t *testing.T) {
	m := newRoleTestApp(t)
	cookie := m.sessionCookie(t, map[string]string{sessionKeyPrincipalType: principalManager, sessionKeyPrincipalID: testManagerID.String()})
	// No GetStaff expectation: an unexpected call fails the test.
	m.pStore.EXPECT().GetManager(gomock.Any(), testManagerID).Return(managerRow(platformdata.ManagerStatusActive), nil)

	if w := m.do(http.MethodGet, "/v1/properties/not-a-uuid", cookie); w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 (manager path past every gate)", w.Code)
	}
}

// routeMinRole is keyed by method and path with nothing tying it to the
// router, so a route that is removed (3i-b retired the billing and account
// routes) would leave a dead entry behind. Every entry must be a registered route.
func TestRouteMinRoleEntriesAreRegisteredRoutes(t *testing.T) {
	m := newRoleTestApp(t)
	registered := map[string]bool{}
	for _, r := range m.router.Routes() {
		registered[r.Method+" "+strings.TrimPrefix(r.Path, "/v1")] = true
	}
	for key := range routeMinRole {
		if !registered[key] {
			t.Errorf("routeMinRole has %q, which is not a registered route: delete it or fix the path", key)
		}
	}
}
