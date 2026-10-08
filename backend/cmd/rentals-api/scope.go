package main

import (
	"strings"

	"github.com/codercollo/willcoll/backend/internal/rentals/data"
	"github.com/codercollo/willcoll/backend/internal/shared/auth"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Property scope, route side (docs/security/authorization.md section 3, D2).
//
// The scope itself is enforced by the database: authenticate() puts a
// restricted staff member's property ids in the request context, every tenant
// transaction hands them to Postgres, and the property_scope policies
// (migration 000047) hide every other row from every query - lists,
// dashboards, reports, exports and search included.
//
// What the database cannot do is turn "no rows" into 404 for operations that
// would otherwise succeed vacuously (generating a run for a property you
// cannot see affects nothing, but must not answer 200). requireResourceInScope
// closes that gap: for a restricted caller, a route naming a resource in its
// path first checks the resource is visible to them and answers 404 if not,
// the same answer as a resource that does not exist.

// scopeKindForRoute maps a registered route path to the resource its :id
// parameter names, from the first path segment. ok is false for a route with
// no resource id (lists, onboarding, the payment-channel banks).
func scopeKindForRoute(fullPath string) (kind data.ScopeKind, ok bool) {
	segs := strings.Split(strings.TrimPrefix(fullPath, "/v1/"), "/")
	if len(segs) < 2 || segs[1] != ":id" {
		return "", false
	}
	switch segs[0] {
	case "properties":
		return data.ScopeProperty, true
	case "units":
		return data.ScopeUnit, true
	case "leases":
		return data.ScopeLease, true
	case "landlords":
		return data.ScopeLandlord, true
	case "payments":
		return data.ScopePayment, true
	case "ledger-entries":
		return data.ScopeLedgerEntry, true
	}
	return "", false
}

// requireResourceInScope is a no-op for everyone who is not property-scoped
// (no query, no cost). Must run after requireActivatedManager.
func (app *application) requireResourceInScope() gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()
		if len(auth.PropertyScopeFrom(ctx)) == 0 {
			c.Next()
			return
		}
		kind, ok := scopeKindForRoute(c.FullPath())
		if !ok {
			c.Next()
			return
		}
		id, err := uuid.Parse(c.Param("id"))
		if err != nil {
			c.Next() // the handler answers a malformed id (404) itself
			return
		}
		tenantID, ok := contextGetTenantID(c)
		if !ok {
			app.authenticationRequiredResponse(c)
			c.Abort()
			return
		}
		found, err := app.models.Scope.Exists(ctx, tenantID, kind, id)
		if err != nil {
			app.serverErrorResponse(c, err)
			c.Abort()
			return
		}
		if !found {
			app.notFoundResponse(c)
			c.Abort()
			return
		}
		c.Next()
	}
}
