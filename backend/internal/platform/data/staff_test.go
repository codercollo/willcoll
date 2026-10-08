package data

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/codercollo/willcoll/backend/internal/platform/db/mock"
	"github.com/codercollo/willcoll/backend/internal/platform/db/sqlc"
	"github.com/codercollo/willcoll/backend/internal/shared/auth"
	"github.com/google/uuid"
	"go.uber.org/mock/gomock"
)

func TestRoleAtLeast(t *testing.T) {
	tests := []struct {
		role, minimum string
		want          bool
	}{
		{RoleOwner, RoleViewer, true},
		{RoleViewer, RoleOwner, false},
		{RoleOfficer, RoleOfficer, true},
		{RoleAccountant, RoleOfficer, true},
		{RoleOfficer, RoleAccountant, false},
		{"bogus", RoleViewer, false},
		{RoleOwner, "bogus", false},
	}
	for _, tc := range tests {
		if got := RoleAtLeast(tc.role, tc.minimum); got != tc.want {
			t.Errorf("RoleAtLeast(%q, %q) = %v, want %v", tc.role, tc.minimum, got, tc.want)
		}
	}
}

func TestStaffModel_InviteThenActivate(t *testing.T) {
	ctrl := gomock.NewController(t)
	store := mock.NewMockStore(ctrl)
	q := mock.NewMockQuerier(ctrl)
	store.EXPECT().ExecTenantTx(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ uuid.UUID, fn func(sqlc.Querier) error) error { return fn(q) }).
		AnyTimes()

	tenantID := uuid.New()
	staffID := uuid.New()

	q.EXPECT().CreateStaff(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ any, a sqlc.CreateStaffParams) (sqlc.Staff, error) {
			if a.Status != StaffInvited {
				t.Errorf("a new staff row must start invited, got %q", a.Status)
			}
			return sqlc.Staff{ID: staffID, ManagerID: a.ManagerID, Name: a.Name, Email: a.Email,
				Role: a.Role, ModuleAccess: a.ModuleAccess, Status: a.Status}, nil
		})

	m := StaffModel{Store: store, AdminStore: store}
	staff := &Staff{Name: "Jane Officer", Email: "jane@example.com", Role: RoleOfficer, ModuleAccess: []string{ProductRentals}}
	if err := m.Invite(context.Background(), tenantID, staff); err != nil {
		t.Fatalf("Invite: %v", err)
	}
	if staff.Status != StaffInvited {
		t.Errorf("status = %q, want invited", staff.Status)
	}

	// Activate (cross-tenant, on AdminStore directly — no ExecTenantTx).
	store.EXPECT().UpdateStaff(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ any, a sqlc.UpdateStaffParams) (int32, error) {
			if a.Status != StaffActive {
				t.Errorf("activate must set status active, got %q", a.Status)
			}
			return a.Version + 1, nil
		})
	if err := staff.Password.Set("correct-horse-battery"); err != nil {
		t.Fatal(err)
	}
	if err := m.Activate(context.Background(), staff); err != nil {
		t.Fatalf("Activate: %v", err)
	}
	if staff.Status != StaffActive {
		t.Errorf("status after activate = %q, want active", staff.Status)
	}
}

func TestStaffModel_FindActiveByEmail_AmbiguousIsNotFound(t *testing.T) {
	ctrl := gomock.NewController(t)
	store := mock.NewMockStore(ctrl)

	store.EXPECT().ListActiveStaffByEmail(gomock.Any(), "shared@example.com").Return([]sqlc.Staff{
		{ID: uuid.New(), ManagerID: uuid.New(), Email: "shared@example.com", Status: StaffActive},
		{ID: uuid.New(), ManagerID: uuid.New(), Email: "shared@example.com", Status: StaffActive},
	}, nil)

	m := StaffModel{AdminStore: store}
	_, err := m.FindActiveByEmail(context.Background(), "shared@example.com")
	if !errors.Is(err, ErrRecordNotFound) {
		t.Fatalf("err = %v, want ErrRecordNotFound for an ambiguous email", err)
	}
}

func TestStaffModel_FindActiveByEmail_NoMatch(t *testing.T) {
	ctrl := gomock.NewController(t)
	store := mock.NewMockStore(ctrl)
	store.EXPECT().ListActiveStaffByEmail(gomock.Any(), "nobody@example.com").Return(nil, nil)

	m := StaffModel{AdminStore: store}
	_, err := m.FindActiveByEmail(context.Background(), "nobody@example.com")
	if !errors.Is(err, ErrRecordNotFound) {
		t.Fatalf("err = %v, want ErrRecordNotFound", err)
	}
}

func TestStaffModel_Get_ScopedToTenant(t *testing.T) {
	// Get runs inside ExecTenantTx(tenantID, ...): asking for manager Y's
	// tenant with manager X's staff id must find nothing, since RLS (not
	// simulated by this mock, but the call shape) scopes the query — this
	// test pins the call shape (id-only WHERE, tenant carried by the tx).
	ctrl := gomock.NewController(t)
	store := mock.NewMockStore(ctrl)
	q := mock.NewMockQuerier(ctrl)
	tenantY := uuid.New()
	staffID := uuid.New()

	store.EXPECT().ExecTenantTx(gomock.Any(), tenantY, gomock.Any()).
		DoAndReturn(func(_ context.Context, _ uuid.UUID, fn func(sqlc.Querier) error) error { return fn(q) })
	q.EXPECT().GetStaff(gomock.Any(), staffID).Return(sqlc.Staff{}, sql.ErrNoRows)

	m := StaffModel{Store: store}
	_, err := m.Get(context.Background(), tenantY, staffID)
	if !errors.Is(err, ErrRecordNotFound) {
		t.Fatalf("err = %v, want ErrRecordNotFound", err)
	}
}

// internal/shared/auth carries its own copy of the four role names (shared
// code cannot import this package); they must match the staff.role CHECK.
func TestRolesMatchSharedAuth(t *testing.T) {
	pairs := [][2]string{
		{RoleViewer, auth.RoleViewer}, {RoleOfficer, auth.RoleOfficer},
		{RoleAccountant, auth.RoleAccountant}, {RoleOwner, auth.RoleOwner},
	}
	for _, p := range pairs {
		if p[0] != p[1] {
			t.Errorf("role %q differs from shared/auth %q", p[0], p[1])
		}
	}
	for _, r := range staffRoles {
		if len(auth.RolePermissions(r)) == 0 {
			t.Errorf("staff role %q has no permissions in shared/auth", r)
		}
	}
}
