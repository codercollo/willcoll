package main

import (
	"github.com/codercollo/willcoll/backend/internal/data"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// gin.Context keys for the request's authenticated principal, set by the
// authenticate middleware from the SCS session (system-design.txt 4.2).
const (
	managerContextKey  = "manager"
	adminContextKey    = "admin"
	tenantIDContextKey = "tenantID"
)

// contextSetManager records the authenticated manager on c, and with it the
// tenant id: a manager's id IS the tenant_id every RLS policy is keyed on
// (system-design.txt 3.7).
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
