package main

import (
	"github.com/codercollo/willcoll/backend/internal/platform/data"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// gin.Context keys for the request's authenticated principal, set by the
// authenticate middleware from the SCS session (system-design.txt A.4).
const (
	managerContextKey  = "manager"
	adminContextKey    = "admin"
	staffContextKey    = "staff"
	tenantIDContextKey = "tenantID"
)

// contextSetManager records the authenticated manager on c, and with it the
// tenant id: a manager's id IS the tenant_id every RLS policy on
// willcoll_platform's manager-scoped tables is keyed on (system-design.txt
// A.2).
func contextSetManager(c *gin.Context, manager *data.Manager) {
	c.Set(managerContextKey, manager)
	if !manager.IsAnonymous() {
		contextSetTenantID(c, manager.ID)
	}
}

// contextGetManager returns the request's manager, or data.AnonymousManager
// when no manager is signed in — never nil (Greenlight's contextGetUser).
func contextGetManager(c *gin.Context) *data.Manager {
	if v, ok := c.Get(managerContextKey); ok {
		if manager, ok := v.(*data.Manager); ok && manager != nil {
			return manager
		}
	}
	return data.AnonymousManager
}

// contextSetStaff records the authenticated staff member on c. The
// underlying manager is set separately via contextSetManager (same row a
// manager-principal request would have), so every existing tenant/RLS/
// activation check keeps working unchanged for a staff session — only
// code that needs to tell a staff request apart from the manager's own
// login (role gates, "owners only" checks) reads this.
func contextSetStaff(c *gin.Context, staff *data.Staff) {
	c.Set(staffContextKey, staff)
}

// contextGetStaff returns the request's staff principal, or
// data.AnonymousStaff when the signed-in principal is the manager's own
// login (the implicit owner) or nobody is signed in.
func contextGetStaff(c *gin.Context) *data.Staff {
	if v, ok := c.Get(staffContextKey); ok {
		if staff, ok := v.(*data.Staff); ok && staff != nil {
			return staff
		}
	}
	return data.AnonymousStaff
}

// contextSetAdmin records the authenticated Super Admin on c.
func contextSetAdmin(c *gin.Context, admin *data.Admin) {
	c.Set(adminContextKey, admin)
}

// contextGetAdmin returns the request's admin, or (nil, false) when the
// request isn't from a signed-in admin.
func contextGetAdmin(c *gin.Context) (*data.Admin, bool) {
	v, ok := c.Get(adminContextKey)
	if !ok {
		return nil, false
	}
	admin, ok := v.(*data.Admin)
	return admin, ok && admin != nil
}

// contextSetTenantID records the tenant scope for tenant-scoped handlers.
// Only contextSetManager calls it outside tests.
func contextSetTenantID(c *gin.Context, tenantID uuid.UUID) {
	c.Set(tenantIDContextKey, tenantID)
}

// contextGetTenantID returns the signed-in manager's tenant id. ok is false
// when no manager is signed in, and handlers must then respond
// authenticationRequiredResponse rather than proceed: RLS makes an
// unscoped query fail at the database anyway, but handlers never rely on
// that as their only guard.
func contextGetTenantID(c *gin.Context) (uuid.UUID, bool) {
	v, exists := c.Get(tenantIDContextKey)
	if !exists {
		return uuid.Nil, false
	}

	id, ok := v.(uuid.UUID)
	return id, ok && id != uuid.Nil
}
