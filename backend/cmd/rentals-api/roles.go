package main

import (
	"github.com/codercollo/willcoll/backend/internal/shared/auth"
	"github.com/codercollo/willcoll/backend/internal/shared/httpx"
	"github.com/gin-gonic/gin"
)

// requirePermission guards one route with one catalog permission
// (internal/shared/auth, docs/security/authorization.md). It replaces the
// old routeMinRole table: every route now names its permission where it is
// registered (routes.go), and auth.Guard refuses a route that doesn't.
// Must run after requireActivatedManager (and, for a staff principal, after
// requireStaffModuleAccess). The permission is resolved per request from the
// staff row authenticate() just loaded, so a demotion takes effect at once.
func (app *application) requirePermission(perm string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if app.permitted(c, perm) {
			c.Next()
			return
		}
		if _, isAdmin := contextGetAdmin(c); !isAdmin && contextGetManager(c).IsAnonymous() {
			app.authenticationRequiredResponse(c)
		} else {
			app.logDenied(c, perm)
			app.notPermittedResponse(c)
		}
		c.Abort()
	}
}

// permitted reports whether the request's principal holds perm: a Super
// Admin only admin.*, the firm's own login the owner set, a staff member
// their role's set (an unknown role holds nothing).
func (app *application) permitted(c *gin.Context, perm string) bool {
	if _, ok := contextGetAdmin(c); ok {
		return auth.AdminPermits(perm)
	}
	if contextGetManager(c).IsAnonymous() {
		return false
	}
	staff := contextGetStaff(c)
	if staff.IsAnonymous() {
		return auth.FirmLoginPermits(perm)
	}
	return auth.StaffPermits(staff.Role, perm)
}

// logDenied notes the permission a request is being refused for. The line
// itself is written, rate limited per actor, by the denialLog middleware
// (httpx.DenialLog, C3.5) in the application log only: a denied call is not a
// business event, and writing each one to audit_log would let a misbehaving
// client flood it.
func (app *application) logDenied(c *gin.Context, perm string) {
	httpx.NoteDeniedPermission(c, perm)
}

// requireOwnerForChannelChange refuses (403) a staff member below owner who
// is changing a property's PayHero channel through a route that is not itself
// owner-gated (POST/PATCH /properties). The channel decides where tenants'
// money lands, so it is owner-only everywhere it can be set, not just on
// POST /properties/:id/payment-channel (system-design.txt A.1). The firm's
// own login is the implicit owner. A permitted change also needs an open
// step-up window (D5): the password prompt comes after the permission check,
// so a caller who may not change the channel never sees it. Reports whether
// the request may go on.
func (app *application) requireOwnerForChannelChange(c *gin.Context, changed bool) bool {
	if !changed {
		return true
	}
	// Routes calling this sit behind requireActivatedManager, so an anonymous
	// staff value is the firm's own login (owner set): only a staff principal
	// can be refused here.
	staff := contextGetStaff(c)
	if !staff.IsAnonymous() && !auth.StaffPermits(staff.Role, auth.PaymentChannelsManage) {
		app.logDenied(c, auth.PaymentChannelsManage)
		app.notPermittedResponse(c)
		return false
	}
	if !app.stepUpSatisfied(c) {
		app.stepUpRequiredResponse(c)
		return false
	}
	return true
}

// channelChangeTarget describes a property's channel change for the owner
// alert, e.g. "Runda Arcade: channel 1234 to none".
func channelChangeTarget(property, oldChannel, newChannel string) string {
	orNone := func(s string) string {
		if s == "" {
			return "none"
		}
		return s
	}
	return property + ": channel " + orNone(oldChannel) + " to " + orNone(newChannel)
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

// denyIfPropertyScoped refuses (403) a request from a staff member restricted
// to specific properties, on the two routes that create firm-wide records: a
// new property or landlord has no place inside a property scope (it would be
// invisible to its own creator), so a restricted member cannot make one
// (authorization.md section 3).
func (app *application) denyIfPropertyScoped(c *gin.Context) {
	if len(auth.PropertyScopeFrom(c.Request.Context())) > 0 {
		app.logDenied(c, "firm-wide create while property-scoped")
		app.notPermittedResponse(c)
		c.Abort()
		return
	}
	c.Next()
}
