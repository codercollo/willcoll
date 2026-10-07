package main

import (
	"net/http"
	"strings"

	platformdata "github.com/codercollo/willcoll/backend/internal/platform/data"
	"github.com/gin-gonic/gin"
)

// requireRole (build-phases.txt 3f-2, system-design.txt A.1): every
// manager route's minimum role, in one table rather than scattered
// per-handler checks. The manager's own login is always the implicit
// owner and always passes; a staff principal needs their Role to rank at
// least the route's minimum (platformdata.RoleAtLeast — an unknown role
// meets no minimum, fails closed).
//
// Routes not listed default by method: GET/HEAD is viewer (read-only,
// system-design.txt A.1), anything else is owner — the safe default for
// a write nobody explicitly classified, rather than silently permissive.
//
// This table is the source of truth; features-functionalities.txt notes
// that it lives here rather than duplicating every row.
var routeMinRole = map[string]string{
	// Officer: day-to-day entry (water readings, unit/lease lookups, cash
	// receipts) — system-design.txt A.1.
	"POST /properties/:id/units":                     platformdata.RoleOfficer,
	"PATCH /units/:id":                               platformdata.RoleOfficer,
	"POST /units/:id/leases":                         platformdata.RoleAccountant, // sets financial lease terms
	"POST /leases/:id/payers":                        platformdata.RoleOfficer,
	"POST /units/:id/rent/payments":                  platformdata.RoleOfficer, // cash receipts
	"PUT /properties/:id/water":                      platformdata.RoleOfficer, // water readings
	"PUT /properties/:id/reports/:period/plot-meter": platformdata.RoleOfficer,

	// Accountant: + generate runs, reversals, allocations, reports, exports.
	"POST /landlords":                               platformdata.RoleAccountant,
	"PATCH /landlords/:id":                          platformdata.RoleAccountant,
	"POST /properties":                              platformdata.RoleAccountant,
	"PATCH /properties/:id":                         platformdata.RoleAccountant,
	"PATCH /properties/:id/garbage":                 platformdata.RoleAccountant,
	"PATCH /properties/:id/electricity":             platformdata.RoleAccountant,
	"GET /properties/:id/electricity/deposits.csv":  platformdata.RoleAccountant,
	"GET /properties/:id/electricity/deposits.pdf":  platformdata.RoleAccountant,
	"PUT /properties/:id/print-theme":               platformdata.RoleAccountant,
	"POST /properties/:id/units/import":             platformdata.RoleAccountant,
	"POST /properties/:id/onboarding/import":        platformdata.RoleAccountant,
	"DELETE /leases/:id/payers/:payerId":            platformdata.RoleAccountant,
	"PATCH /leases/:id":                             platformdata.RoleAccountant,
	"POST /leases/:id/qr/rotate":                    platformdata.RoleAccountant,
	"POST /properties/:id/qr/bulk":                  platformdata.RoleAccountant,
	"PUT /properties/:id/rent-schedule":             platformdata.RoleAccountant,
	"POST /properties/:id/rent/generate":            platformdata.RoleAccountant,
	"POST /units/:id/rent/charges":                  platformdata.RoleAccountant,
	"POST /ledger-entries/:id/reverse":              platformdata.RoleAccountant, // reversals
	"POST /payments/:id/allocate":                   platformdata.RoleAccountant, // allocations
	"POST /payments/:id/confirm":                    platformdata.RoleAccountant,
	"POST /properties/:id/water/generate":           platformdata.RoleAccountant,
	"GET /properties/:id/water/invoices/:period":    platformdata.RoleAccountant, // export
	"POST /properties/:id/garbage/generate":         platformdata.RoleAccountant,
	"GET /properties/:id/garbage/invoices/:period":  platformdata.RoleAccountant, // export
	"GET /properties/:id/reports/:period":           platformdata.RoleAccountant, // reports
	"GET /properties/:id/reports/:period/checks":    platformdata.RoleAccountant,
	"POST /properties/:id/reports/:period/confirm":  platformdata.RoleAccountant,
	"POST /properties/:id/reports/:period/generate": platformdata.RoleAccountant,
	"POST /units/:id/arrears-commitments":           platformdata.RoleAccountant,
	"GET /properties/:id/receipts/:period/download": platformdata.RoleAccountant, // export

	// Owner only: payment-channel setup and archiving a property
	// (system-design.txt A.1). Staff, billing and modules are platform-api's.
	"POST /properties/:id/payment-channel":      platformdata.RoleOwner,
	"POST /properties/:id/payment-channel/test": platformdata.RoleOwner,
	"DELETE /properties/:id":                    platformdata.RoleOwner, // archiving a whole property
}

// minRoleFor looks up method+fullPath's minimum role, falling back to the
// safe by-method default (see routeMinRole's doc comment) when unlisted.
func minRoleFor(method, fullPath string) string {
	fullPath = strings.TrimPrefix(fullPath, "/v1")
	if role, ok := routeMinRole[method+" "+fullPath]; ok {
		return role
	}
	if method == http.MethodGet || method == http.MethodHead {
		return platformdata.RoleViewer
	}
	return platformdata.RoleOwner
}

// requireRole enforces routeMinRole for every manager route. Must run
// after requireActivatedManager (and, for a staff principal, after
// requireStaffModuleAccess).
func (app *application) requireRole() gin.HandlerFunc {
	return func(c *gin.Context) {
		staff := contextGetStaff(c)
		if staff.IsAnonymous() {
			c.Next() // the manager's own login: always the implicit owner
			return
		}

		minRole := minRoleFor(c.Request.Method, c.FullPath())
		if !platformdata.RoleAtLeast(staff.Role, minRole) {
			app.notPermittedResponse(c)
			c.Abort()
			return
		}
		c.Next()
	}
}

// requireOwnerForChannelChange refuses (403) a staff member below owner who
// is changing a property's PayHero channel through a route that is not itself
// owner-gated (POST/PATCH /properties). The channel decides where tenants'
// money lands, so it is owner-only everywhere it can be set, not just on
// POST /properties/:id/payment-channel (system-design.txt A.1). The firm's
// own login is the implicit owner. Reports whether the request may go on.
func (app *application) requireOwnerForChannelChange(c *gin.Context, changed bool) bool {
	staff := contextGetStaff(c)
	if !changed || staff.IsAnonymous() || platformdata.RoleAtLeast(staff.Role, platformdata.RoleOwner) {
		return true
	}
	app.notPermittedResponse(c)
	return false
}

// requireStaffModuleAccess guards the manager route group against a staff
// principal whose module_access doesn't include product — a plain
// system-design.txt A.1 403, independent of role. The manager's own
// login always passes (module_access is a staff-only concept). Must run
// after requireActivatedManager.
func (app *application) requireStaffModuleAccess(product string) gin.HandlerFunc {
	return func(c *gin.Context) {
		staff := contextGetStaff(c)
		if staff.IsAnonymous() {
			c.Next()
			return
		}
		for _, p := range staff.ModuleAccess {
			if p == product {
				c.Next()
				return
			}
		}
		app.notPermittedResponse(c)
		c.Abort()
	}
}
