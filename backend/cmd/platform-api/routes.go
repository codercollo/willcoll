package main

import (
	"github.com/codercollo/willcoll/backend/internal/shared/auth"
	"github.com/gin-gonic/gin"
)

// routes wires platform-api's v1 surface. Identity (signup/activation/
// sessions/tokens/account) landed in build-phases.txt 3c-ii. Billing,
// admin oversight and plan management land in 3c-iii/3c-iv, moved over
// from cmd/rentals-api with the same request/response shapes where
// billing.AccessFor's dependency doesn't force a gap (see sessions.go and
// account.go's KNOWN GAP notes).
func (app *application) routes() (*gin.Engine, error) {
	router := gin.New()

	// Every route is registered through guard (a catalog permission) or
	// public (a reviewed allowlist entry, public_routes.go); registering any
	// other way fails TestEveryRouteIsGuardedOrPublic.
	app.routeGuards = auth.NewRouteTable()
	guard := func(rg *gin.RouterGroup, method, path, perm string, handlers ...gin.HandlerFunc) {
		auth.Guard(app.routeGuards, rg, method, path, perm, app.requirePermission, handlers...)
	}
	public := func(rg *gin.RouterGroup, method, path string, handlers ...gin.HandlerFunc) {
		auth.Public(app.routeGuards, rg, publicRoutes, method, path, handlers...)
	}

	// Only X-Forwarded-For from these proxies (Caddy) is believed by
	// c.ClientIP(). Gin trusts every proxy by default, which would let any
	// caller pick their own IP and dodge rate limiting.
	if err := router.SetTrustedProxies(app.config.trustedProxies); err != nil {
		return nil, err
	}

	// system-design.txt A.5 chain; rateLimit is applied per group below.
	router.Use(
		app.recoverPanic(),
		app.logRequest(),
		app.metrics(),
		app.loadAndSaveSession(),
		app.authenticate(),
		app.enableCORS(),
	)

	router.NoRoute(func(c *gin.Context) { app.notFoundResponse(c) })
	router.NoMethod(func(c *gin.Context) { app.methodNotAllowedResponse(c) })

	// /debug/vars is NOT on this router: it is served by the internal
	// metrics listener only (httpx.NewMetricsServer, METRICS_ADDR), so the
	// public surface has no /debug/* route.

	// One limiter shared by every rate-limited route, so a client can't
	// multiply its budget by spreading requests across them.
	rateLimited := app.rateLimit()

	v1 := router.Group("/v1")
	{
		public(v1, "GET", "/healthcheck", app.healthcheckHandler)

		// -- Auth & manager lifecycle. Public; the credential and
		// email-sending endpoints are rate limited. --
		public(v1, "POST", "/managers", rateLimited, app.registerManagerHandler)
		public(v1, "PUT", "/managers/activated", app.activateManagerHandler)
		public(v1, "POST", "/tokens/activation", rateLimited, app.createActivationTokenHandler)
		public(v1, "PUT", "/staff/activated", app.activateStaffHandler)
		public(v1, "POST", "/sessions", rateLimited, app.createSessionHandler)
		public(v1, "GET", "/sessions", app.showSessionHandler)
		public(v1, "DELETE", "/sessions", app.deleteSessionHandler)
		// Super Admin login: outside the /admin group's guard, since the
		// admin isn't signed in yet.
		public(v1, "POST", "/admin/sessions", rateLimited, app.createAdminSessionHandler)
		public(v1, "POST", "/tokens/password-reset", rateLimited, app.createPasswordResetTokenHandler)
		public(v1, "PUT", "/managers/password", rateLimited, app.updateManagerPasswordHandler)

		// -- Account: an activated OR pending manager's own profile. No
		// requireSubscription() here — that gate is per-product now
		// (system-design.txt A.5) and lives in each product API, not here. --
		manager := v1.Group("", app.requireActivatedManager())
		{
			guard(manager, "GET", "/account", auth.AccountRead, app.showAccountHandler)
			// Owner only: the firm profile is the firm's, not a staff member's.
			guard(manager, "PATCH", "/account", auth.AccountManage, app.updateAccountHandler)
			guard(manager, "PUT", "/account/password", auth.AccountPassword, rateLimited, app.changePasswordHandler)
			guard(manager, "PUT", "/staff/me/password", auth.StaffSelfPassword, rateLimited, app.changeStaffPasswordHandler)
			guard(manager, "GET", "/modules", auth.ModulesRead, app.listModulesHandler)

			// -- Owner only: staff, billing, modules, plans
			// (system-design.txt A.1, build-phases.txt 3f-1). The manager's
			// own login is always the implicit owner. --
			{
				guard(manager, "POST", "/staff", auth.StaffManage, app.inviteStaffHandler)
				guard(manager, "GET", "/staff", auth.StaffRead, app.listStaffHandler)
				guard(manager, "POST", "/modules/:product/trial", auth.ModulesManage, app.startModuleTrialHandler)
				guard(manager, "PATCH", "/staff/:id", auth.StaffManage, app.updateStaffHandler)

				// Platform billing (build-phases.txt 3c-iv-b1): the one
				// subscriptions/invoices implementation, scoped by :product
				// so a manager can hold an independent subscription per
				// product.
				guard(manager, "GET", "/billing/:product/plans", auth.BillingRead, app.listSubscriptionPlansHandler)
				guard(manager, "GET", "/billing/:product/subscription", auth.BillingRead, app.showSubscriptionHandler)
				guard(manager, "POST", "/billing/:product/subscription/renew", auth.BillingManage, app.renewSubscriptionHandler)
				guard(manager, "GET", "/billing/:product/invoices", auth.BillingRead, app.listSubscriptionInvoicesHandler)
				guard(manager, "GET", "/billing/:product/invoices/:id/pdf", auth.BillingRead, app.showSubscriptionInvoicePDFHandler)
			}
		}

		// -- PayHero subscription webhook: server to server, verified in the
		// handler, and exempt from rate limiting (system-design.txt A.6). --
		public(v1, "POST", "/webhooks/payhero/subscriptions", app.payheroSubscriptionsWebhookHandler)

		// -- Super Admin: plan management (build-phases.txt 3c-iii) and
		// manager/subscription oversight (3i-b). --
		admin := v1.Group("/admin", app.requireAdmin())
		{
			guard(admin, "GET", "/managers", auth.AdminManagersRead, app.adminListManagersHandler)
			guard(admin, "GET", "/managers/:id", auth.AdminManagersRead, app.adminShowManagerHandler)
			guard(admin, "POST", "/managers/:id/suspend", auth.AdminManagersSuspend, app.adminSuspendManagerHandler)
			guard(admin, "POST", "/managers/:id/reinstate", auth.AdminManagersSuspend, app.adminReinstateManagerHandler)
			guard(admin, "GET", "/subscriptions", auth.AdminSubscriptionsRead, app.adminListSubscriptionsHandler)
			guard(admin, "GET", "/system/health", auth.AdminSystemRead, app.adminSystemHealthHandler)
			guard(admin, "GET", "/plans", auth.AdminPlansRead, app.adminListPlansHandler)
			guard(admin, "POST", "/plans", auth.AdminPlansManage, app.adminCreatePlanHandler)
			guard(admin, "PATCH", "/plans/:id", auth.AdminPlansManage, app.adminUpdatePlanHandler)
			guard(admin, "POST", "/plans/:id/archive", auth.AdminPlansManage, app.adminArchivePlanHandler)
			guard(admin, "POST", "/plans/:id/restore", auth.AdminPlansManage, app.adminRestorePlanHandler)
		}
	}

	return router, nil
}
