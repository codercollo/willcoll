package auth

import "sort"

// Permission catalog and role -> permission map (docs/security/
// authorization.md, sections 2-3, approved D1-D5). This file is the single
// source of truth for who may do what: versioned and code-reviewed, never
// editable in the database (the DB stores only a staff member's role and
// scopes). A change here is a change to the approved matrix and needs a
// new review.
//
// Permissions are "resource.action" strings. Roles are cumulative
// (viewer < officer < accountant < owner); the owner block is never given
// to anyone below owner. The firm's own login is the implicit owner plus
// AccountPassword. A Super Admin holds admin.* only and no firm permission.

// Roles a staff member can hold (the platform staff.role CHECK constraint
// carries the same four strings; a test in internal/platform/data pins them).
const (
	RoleViewer     = "viewer"
	RoleOfficer    = "officer"
	RoleAccountant = "accountant"
	RoleOwner      = "owner"
)

// rentals-api permissions.
const (
	LandlordsRead  = "landlords.read"
	LandlordsWrite = "landlords.write"

	PropertiesRead   = "properties.read"
	PropertiesWrite  = "properties.write"
	PropertiesDelete = "properties.delete"

	OnboardingRead   = "onboarding.read"
	OnboardingImport = "onboarding.import"

	PaymentChannelsRead   = "payment_channels.read"
	PaymentChannelsManage = "payment_channels.manage"

	UnitsRead   = "units.read"
	UnitsWrite  = "units.write"
	UnitsImport = "units.import"

	TenantsRead  = "tenants.read"
	TenantsWrite = "tenants.write" // add a payer
	LeasesWrite  = "leases.write"  // create/edit lease, remove payer (D1: accountant)

	QRRead   = "qr.read"
	QRManage = "qr.manage"

	RentRead          = "rent.read"
	LedgerRead        = "ledger.read"
	RentScheduleWrite = "rent_schedule.write"

	ChargesGenerate = "charges.generate"
	ChargesPreview  = "charges.preview"
	ChargesWrite    = "charges.write"
	ChargesReverse  = "charges.reverse"
	ArrearsWrite    = "arrears.write"

	PaymentsRead     = "payments.read"
	PaymentsRecord   = "payments.record"
	PaymentsAllocate = "payments.allocate"

	WaterRead      = "water.read"
	WaterWrite     = "water.write"
	InvoicesRead   = "invoices.read"
	PlotMeterRead  = "plot_meter.read"
	PlotMeterWrite = "plot_meter.write"

	ReportsRead    = "reports.read"
	ReportsConfirm = "reports.confirm"
	ReceiptsRead   = "receipts.read"
	ExportsCreate  = "exports.create"
	DepositsRead   = "deposits.read"

	// Reserved: no route yet (OTP SMS is a system action; the audit log page
	// is Phase 5k).
	SMSSend   = "sms.send"
	AuditRead = "audit.read"
)

// platform-api permissions.
const (
	AccountRead     = "account.read"
	AccountManage   = "account.manage"
	AccountPassword = "account.password" // the firm login only, never a staff role

	StaffSelfPassword = "staff.self_password" // any staff role; not the firm login

	ModulesRead   = "modules.read"
	ModulesManage = "modules.manage"

	StaffRead   = "staff.read"
	StaffManage = "staff.manage"

	BillingRead   = "billing.read"
	BillingManage = "billing.manage"
)

// Super Admin permissions (admin.* only).
const (
	AdminManagersRead      = "admin.managers.read"
	AdminManagersSuspend   = "admin.managers.suspend"
	AdminSubscriptionsRead = "admin.subscriptions.read"
	AdminPlansRead         = "admin.plans.read"
	AdminPlansManage       = "admin.plans.manage"
	AdminSystemRead        = "admin.system.read"
)

var viewerPermissions = []string{
	LandlordsRead, PropertiesRead, OnboardingRead, PaymentChannelsRead,
	UnitsRead, TenantsRead, RentRead, LedgerRead, WaterRead, PlotMeterRead,
	AccountRead, ModulesRead, StaffSelfPassword,
}

var officerPermissions = []string{
	QRRead, PaymentsRead, ChargesPreview, DepositsRead, // D4: moved up from viewer
	UnitsWrite, TenantsWrite, PaymentsRecord, WaterWrite, PlotMeterWrite,
}

var accountantPermissions = []string{
	LandlordsWrite, PropertiesWrite, OnboardingImport, UnitsImport, LeasesWrite,
	QRManage, RentScheduleWrite, ChargesGenerate, ChargesWrite, ChargesReverse,
	ArrearsWrite, PaymentsAllocate, InvoicesRead, ReportsRead, ReportsConfirm,
	ReceiptsRead, ExportsCreate, SMSSend,
}

var ownerPermissions = []string{
	PropertiesDelete, PaymentChannelsManage, AccountManage, ModulesManage,
	StaffRead, StaffManage, BillingRead, BillingManage, AuditRead,
}

var adminPermissions = []string{
	AdminManagersRead, AdminManagersSuspend, AdminSubscriptionsRead,
	AdminPlansRead, AdminPlansManage, AdminSystemRead,
}

var (
	rolePermissions = buildRolePermissions()
	firmLogin       = firmLoginSet()
	admin           = setOf(adminPermissions)
	catalog         = buildCatalog()
)

func setOf(lists ...[]string) map[string]bool {
	s := map[string]bool{}
	for _, l := range lists {
		for _, p := range l {
			s[p] = true
		}
	}
	return s
}

func buildRolePermissions() map[string]map[string]bool {
	v := setOf(viewerPermissions)
	o := setOf(viewerPermissions, officerPermissions)
	a := setOf(viewerPermissions, officerPermissions, accountantPermissions)
	w := setOf(viewerPermissions, officerPermissions, accountantPermissions, ownerPermissions)
	return map[string]map[string]bool{RoleViewer: v, RoleOfficer: o, RoleAccountant: a, RoleOwner: w}
}

func permissionsFor(role string) []string {
	var out []string
	for p := range rolePermissions[role] {
		out = append(out, p)
	}
	return out
}

// firmLoginSet is the owner set plus AccountPassword, minus
// StaffSelfPassword (a staff-only action: the firm login has no staff row).
func firmLoginSet() map[string]bool {
	s := setOf(permissionsFor(RoleOwner), []string{AccountPassword})
	delete(s, StaffSelfPassword)
	return s
}

func buildCatalog() map[string]bool {
	return setOf(viewerPermissions, officerPermissions, accountantPermissions, ownerPermissions,
		[]string{AccountPassword}, adminPermissions)
}

// StaffPermits reports whether a staff member holding role may do perm. An
// unknown role holds nothing (fails closed).
func StaffPermits(role, perm string) bool { return rolePermissions[role][perm] }

// FirmLoginPermits reports whether the firm's own login (the implicit owner)
// may do perm. Only the firm login holds AccountPassword.
func FirmLoginPermits(perm string) bool { return firmLogin[perm] }

// AdminPermits reports whether a Super Admin may do perm (admin.* only).
func AdminPermits(perm string) bool { return admin[perm] }

// Known reports whether perm is in the catalog. A route registered with an
// unknown permission is a programming error the route-table test catches.
func Known(perm string) bool { return catalog[perm] }

// Catalog returns every permission, sorted.
func Catalog() []string {
	out := make([]string, 0, len(catalog))
	for p := range catalog {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// RolePermissions returns the sorted permissions of a staff role (nil for an
// unknown role). 4-auth-3's GET /v1/me serves this to the frontend.
func RolePermissions(role string) []string {
	var out []string
	for p := range rolePermissions[role] {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// FirmLoginPermissions returns the sorted permissions of the firm's own login.
func FirmLoginPermissions() []string {
	out := make([]string, 0, len(firmLogin))
	for p := range firmLogin {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}
