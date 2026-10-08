package auth

import (
	"strings"
	"testing"
)

func TestRolesAreCumulative(t *testing.T) {
	order := []string{RoleViewer, RoleOfficer, RoleAccountant, RoleOwner}
	for i := 1; i < len(order); i++ {
		for p := range rolePermissions[order[i-1]] {
			if !rolePermissions[order[i]][p] {
				t.Errorf("%s lacks %s, which %s holds", order[i], p, order[i-1])
			}
		}
		if len(rolePermissions[order[i]]) <= len(rolePermissions[order[i-1]]) {
			t.Errorf("%s adds nothing over %s", order[i], order[i-1])
		}
	}
}

func TestEveryRolePermissionIsInCatalog(t *testing.T) {
	for role, set := range rolePermissions {
		for p := range set {
			if !Known(p) {
				t.Errorf("%s holds %q, not in the catalog", role, p)
			}
		}
	}
}

// The approved matrix's explicit denials (docs/security/authorization.md).
func TestMatrixDenials(t *testing.T) {
	deny := []struct {
		role  string
		perms []string
	}{
		{RoleViewer, []string{ExportsCreate, SMSSend, QRRead, PaymentsRead, ChargesPreview, DepositsRead, PaymentsRecord, WaterWrite, ChargesReverse}},
		{RoleOfficer, []string{ChargesReverse, PaymentChannelsManage, StaffRead, StaffManage, BillingRead, BillingManage, LeasesWrite, ExportsCreate}},
		{RoleAccountant, []string{PaymentChannelsManage, StaffManage, StaffRead, BillingManage, BillingRead, AccountManage, ModulesManage, PropertiesDelete, AuditRead, AccountPassword}},
		{RoleOwner, []string{AccountPassword, AdminManagersRead, AdminPlansManage}}, // staff owner is not the firm login
	}
	for _, d := range deny {
		for _, p := range d.perms {
			if StaffPermits(d.role, p) {
				t.Errorf("%s must not hold %s", d.role, p)
			}
		}
	}
	allow := []struct{ role, perm string }{
		{RoleOfficer, PaymentsRecord}, {RoleOfficer, WaterWrite}, {RoleOfficer, TenantsWrite},
		{RoleAccountant, ChargesReverse}, {RoleAccountant, PaymentsRecord}, {RoleAccountant, ExportsCreate},
		{RoleOwner, PaymentChannelsManage}, {RoleOwner, StaffManage}, {RoleOwner, BillingManage},
		{RoleViewer, PropertiesRead}, {RoleViewer, StaffSelfPassword},
	}
	for _, a := range allow {
		if !StaffPermits(a.role, a.perm) {
			t.Errorf("%s must hold %s", a.role, a.perm)
		}
	}
}

func TestUnknownRoleHoldsNothing(t *testing.T) {
	for _, role := range []string{"", "root", "Owner", "admin"} {
		if StaffPermits(role, PropertiesRead) || StaffPermits(role, AccountRead) {
			t.Errorf("unknown role %q holds a permission", role)
		}
	}
}

func TestFirmLoginAndAdmin(t *testing.T) {
	if !FirmLoginPermits(AccountPassword) || !FirmLoginPermits(PaymentChannelsManage) {
		t.Error("firm login must hold the owner set plus account.password")
	}
	if FirmLoginPermits(StaffSelfPassword) {
		t.Error("firm login has no staff row, so no staff.self_password")
	}
	for _, p := range Catalog() {
		isAdmin := strings.HasPrefix(p, "admin.")
		if AdminPermits(p) != isAdmin {
			t.Errorf("AdminPermits(%s) = %v, want %v (Super Admin holds admin.* only)", p, AdminPermits(p), isAdmin)
		}
		if isAdmin && (FirmLoginPermits(p) || StaffPermits(RoleOwner, p)) {
			t.Errorf("firm principals must not hold %s", p)
		}
	}
}

func TestRouteTableProblems(t *testing.T) {
	tbl := NewRouteTable()
	tbl.record("GET /ok", RouteGuard{Permission: PropertiesRead})
	tbl.record("GET /pub", RouteGuard{PublicReason: "test"})
	tbl.record("GET /bad", RouteGuard{Permission: "nope.read"})
	tbl.record("GET /ghost", RouteGuard{Permission: PropertiesRead})

	got := tbl.Problems(
		[]string{"GET /ok", "GET /pub", "GET /bad", "GET /unguarded"},
		map[string]string{"GET /pub": "test", "GET /stale": "gone"},
	)
	want := []string{
		"GET /bad: unknown permission nope.read",
		"GET /ghost: in the route table but not registered",
		"GET /stale: in the public allowlist but not registered as public",
		"GET /unguarded: registered without a permission or public allowlist entry",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("problems =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}
