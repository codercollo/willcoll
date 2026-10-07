package main

import (
	"expvar"

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

	// expvar metrics. Caddy only exposes this to localhost/VPN; not part
	// of the public API.
	router.GET("/debug/vars", gin.WrapH(expvar.Handler()))

	// One limiter shared by every rate-limited route, so a client can't
	// multiply its budget by spreading requests across them.
	rateLimited := app.rateLimit()

	v1 := router.Group("/v1")
	{
		v1.GET("/healthcheck", app.healthcheckHandler)

		// -- Auth & manager lifecycle. Public; the credential and
		// email-sending endpoints are rate limited. --
		v1.POST("/managers", rateLimited, app.registerManagerHandler)
		v1.PUT("/managers/activated", app.activateManagerHandler)
		v1.POST("/tokens/activation", rateLimited, app.createActivationTokenHandler)
		v1.PUT("/staff/activated", app.activateStaffHandler)
		v1.POST("/sessions", rateLimited, app.createSessionHandler)
		v1.GET("/sessions", app.showSessionHandler)
		v1.DELETE("/sessions", app.deleteSessionHandler)
		// Super Admin login: outside the /admin group's guard, since the
		// admin isn't signed in yet.
		v1.POST("/admin/sessions", rateLimited, app.createAdminSessionHandler)
		v1.POST("/tokens/password-reset", rateLimited, app.createPasswordResetTokenHandler)
		v1.PUT("/managers/password", rateLimited, app.updateManagerPasswordHandler)

		// -- Account: an activated OR pending manager's own profile. No
		// requireSubscription() here — that gate is per-product now
		// (system-design.txt A.5) and lives in each product API, not here. --
		manager := v1.Group("", app.requireActivatedManager())
		{
			manager.GET("/account", app.showAccountHandler)
			// Owner only: the firm profile is the firm's, not a staff member's.
			manager.PATCH("/account", app.requireOwner(), app.updateAccountHandler)
			manager.PUT("/account/password", rateLimited, app.changePasswordHandler)
			manager.PUT("/staff/me/password", rateLimited, app.changeStaffPasswordHandler)
			manager.GET("/modules", app.listModulesHandler)

			// -- Owner only: staff, billing, modules, plans
			// (system-design.txt A.1, build-phases.txt 3f-1). The manager's
			// own login is always the implicit owner. --
			owner := manager.Group("", app.requireOwner())
			{
				owner.POST("/staff", app.inviteStaffHandler)
				owner.GET("/staff", app.listStaffHandler)
				owner.POST("/modules/:product/trial", app.startModuleTrialHandler)
				owner.PATCH("/staff/:id", app.updateStaffHandler)

				// Platform billing (build-phases.txt 3c-iv-b1): the one
				// subscriptions/invoices implementation, scoped by :product
				// so a manager can hold an independent subscription per
				// product.
				owner.GET("/billing/:product/plans", app.listSubscriptionPlansHandler)
				owner.GET("/billing/:product/subscription", app.showSubscriptionHandler)
				owner.POST("/billing/:product/subscription/renew", app.renewSubscriptionHandler)
				owner.GET("/billing/:product/invoices", app.listSubscriptionInvoicesHandler)
				owner.GET("/billing/:product/invoices/:id/pdf", app.showSubscriptionInvoicePDFHandler)
			}
		}

		// -- PayHero subscription webhook: server to server, verified in the
		// handler, and exempt from rate limiting (system-design.txt A.6). --
		v1.POST("/webhooks/payhero/subscriptions", app.payheroSubscriptionsWebhookHandler)

		// -- Super Admin: plan management (build-phases.txt 3c-iii) and
		// manager/subscription oversight (3i-b). --
		admin := v1.Group("/admin", app.requireAdmin())
		{
			admin.GET("/managers", app.adminListManagersHandler)
			admin.GET("/managers/:id", app.adminShowManagerHandler)
			admin.POST("/managers/:id/suspend", app.adminSuspendManagerHandler)
			admin.POST("/managers/:id/reinstate", app.adminReinstateManagerHandler)
			admin.GET("/subscriptions", app.adminListSubscriptionsHandler)
			admin.GET("/system/health", app.adminSystemHealthHandler)
			admin.GET("/plans", app.adminListPlansHandler)
			admin.POST("/plans", app.adminCreatePlanHandler)
			admin.PATCH("/plans/:id", app.adminUpdatePlanHandler)
			admin.POST("/plans/:id/archive", app.adminArchivePlanHandler)
			admin.POST("/plans/:id/restore", app.adminRestorePlanHandler)
		}
	}

	return router, nil
}
