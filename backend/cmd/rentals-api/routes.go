package main

import (
	"github.com/codercollo/willcoll/backend/internal/shared/auth"
	"github.com/gin-gonic/gin"
)

// routes wires the full v1 API surface, grouped by the system-design.txt
// section that specifies each group. Every route has a working handler: none
// is a placeholder.
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

	// system-design.txt 4.2 chain; rateLimit is applied per group below.
	router.Use(
		app.recoverPanic(),
		app.logRequest(),
		app.metrics(),
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

	// Public QR scan, outside /v1: the branded domain's /q/* is routed here by
	// the reverse proxy. Rate limited like the pay surface.
	public(&router.RouterGroup, "GET", "/q/:token", rateLimited, app.scanUnitQRHandler)

	v1 := router.Group("/v1")
	{
		public(v1, "GET", "/healthcheck", app.healthcheckHandler)

		// -- Manager dashboard: an activated manager's session, and every
		// query inside that manager's RLS scope (system-design.txt 4.5).
		// requireModule/requirePlatformSubscription read the module
		// entitlement and trial/subscription from willcoll_platform and
		// always enforce (build-phases.txt 3i-b).
		// requireStaffModuleAccess (3f-2) is a no-op for the
		// manager's own login (always the implicit owner) and only
		// matter once a staff member signs in. --
		manager := v1.Group("", app.requireActivatedManager(), app.requireStaffModuleAccess("rentals"),
			app.requireModule("rentals"), app.requirePlatformSubscription("rentals"),
			app.requireResourceInScope())
		{
			// Core resources (system-design.txt 4.6)
			guard(manager, "GET", "/landlords", auth.LandlordsRead, app.listLandlordsHandler)
			guard(manager, "POST", "/landlords", auth.LandlordsWrite, app.denyIfPropertyScoped, app.createLandlordHandler)
			guard(manager, "GET", "/landlords/:id", auth.LandlordsRead, app.showLandlordHandler)
			guard(manager, "PATCH", "/landlords/:id", auth.LandlordsWrite, app.updateLandlordHandler)

			guard(manager, "GET", "/properties", auth.PropertiesRead, app.listPropertiesHandler)
			guard(manager, "POST", "/properties", auth.PropertiesWrite, app.denyIfPropertyScoped, app.createPropertyHandler)
			guard(manager, "GET", "/properties/:id", auth.PropertiesRead, app.showPropertyHandler)
			guard(manager, "PATCH", "/properties/:id", auth.PropertiesWrite, app.updatePropertyHandler)
			guard(manager, "DELETE", "/properties/:id", auth.PropertiesDelete, app.deletePropertyHandler) // soft delete (archive)
			guard(manager, "PATCH", "/properties/:id/garbage", auth.PropertiesWrite, app.updatePropertyGarbageHandler)
			guard(manager, "PATCH", "/properties/:id/electricity", auth.PropertiesWrite, app.updatePropertyElectricityHandler)
			guard(manager, "GET", "/properties/:id/electricity/deposits", auth.DepositsRead, app.showElectricityDepositsHandler)
			guard(manager, "GET", "/properties/:id/electricity/deposits.csv", auth.ExportsCreate, app.showElectricityDepositsCSVHandler)
			guard(manager, "GET", "/properties/:id/electricity/deposits.pdf", auth.ExportsCreate, app.showElectricityDepositsPDFHandler)
			guard(manager, "PUT", "/properties/:id/print-theme", auth.PropertiesWrite, app.updatePropertyPrintThemeHandler)
			// Step-up (D5, internal/shared/auth/stepup.go): the money-moving
			// actions below need a recent password re-entry on top of the
			// permission. Property create/patch check it only when they
			// actually change the channel (requireOwnerForChannelChange).
			guard(manager, "POST", "/properties/:id/payment-channel", auth.PaymentChannelsManage, app.requireStepUp(), app.registerPaymentChannelHandler)
			guard(manager, "GET", "/properties/:id/payment-channel/status", auth.PaymentChannelsRead, app.showPaymentChannelStatusHandler)
			guard(manager, "POST", "/properties/:id/payment-channel/test", auth.PaymentChannelsManage, app.requireStepUp(), app.sendPaymentChannelTestHandler)
			guard(manager, "GET", "/payment-channel-banks", auth.PaymentChannelsRead, app.listPaymentChannelBanksHandler)

			guard(manager, "GET", "/properties/:id/units", auth.UnitsRead, app.listUnitsForPropertyHandler)
			guard(manager, "POST", "/properties/:id/units", auth.UnitsWrite, app.createUnitHandler)
			guard(manager, "POST", "/properties/:id/units/import", auth.UnitsImport, app.importUnitsHandler)
			guard(manager, "GET", "/onboarding", auth.OnboardingRead, app.showOnboardingHandler)
			guard(manager, "POST", "/properties/:id/onboarding/import", auth.OnboardingImport, app.onboardingImportHandler) // units + tenants + opening balances
			guard(manager, "GET", "/units/:id", auth.UnitsRead, app.showUnitHandler)
			guard(manager, "PATCH", "/units/:id", auth.UnitsWrite, app.updateUnitHandler)

			guard(manager, "POST", "/units/:id/leases", auth.LeasesWrite, app.createLeaseHandler)
			guard(manager, "GET", "/units/:id/leases", auth.TenantsRead, app.listUnitLeasesHandler)
			guard(manager, "POST", "/leases/:id/payers", auth.TenantsWrite, app.addLeasePayerHandler)
			guard(manager, "DELETE", "/leases/:id/payers/:payerId", auth.LeasesWrite, app.removeLeasePayerHandler)
			guard(manager, "GET", "/leases/:id", auth.TenantsRead, app.showLeaseHandler)
			guard(manager, "PATCH", "/leases/:id", auth.LeasesWrite, app.updateLeaseHandler)
			// Lease-bound unit QR codes: the sticker on the door.
			guard(manager, "GET", "/leases/:id/qr", auth.QRRead, app.showUnitQRHandler)
			guard(manager, "GET", "/leases/:id/qr.png", auth.QRRead, app.showUnitQRImageHandler)
			guard(manager, "POST", "/leases/:id/qr/rotate", auth.QRManage, app.rotateUnitQRHandler)
			guard(manager, "POST", "/properties/:id/qr/bulk", auth.QRManage, app.bulkGenerateQRHandler)

			// Billing engines, ledgers, reports (system-design.txt 4.6)
			guard(manager, "PUT", "/properties/:id/rent-schedule", auth.RentScheduleWrite, app.updateRentScheduleHandler)
			guard(manager, "POST", "/properties/:id/rent/generate", auth.ChargesGenerate, app.generateRentChargesHandler)
			guard(manager, "GET", "/properties/:id/rent/overview", auth.RentRead, app.showRentOverviewHandler)
			guard(manager, "POST", "/units/:id/rent/payments", auth.PaymentsRecord, app.createRentPaymentHandler)
			guard(manager, "POST", "/units/:id/rent/charges", auth.ChargesWrite, app.createRentChargeHandler)
			guard(manager, "POST", "/ledger-entries/:id/reverse", auth.ChargesReverse, app.requireStepUp(), app.reverseLedgerEntryHandler)

			// Payment review queue (system-design.txt 4.8, 5)
			guard(manager, "GET", "/payments/review", auth.PaymentsRead, app.listPaymentsForReviewHandler)
			guard(manager, "POST", "/payments/:id/allocate", auth.PaymentsAllocate, app.allocatePaymentHandler)
			guard(manager, "POST", "/payments/:id/confirm", auth.PaymentsAllocate, app.confirmPaymentHandler)

			guard(manager, "GET", "/properties/:id/water", auth.WaterRead, app.showWaterReadingsHandler)
			guard(manager, "PUT", "/properties/:id/water", auth.WaterWrite, app.updateWaterReadingsHandler)
			guard(manager, "POST", "/properties/:id/water/generate", auth.ChargesGenerate, app.generateWaterChargesHandler)
			// Gin params can't carry a literal suffix, so :period arrives as
			// "<period>.pdf" and the handler strips the extension.
			guard(manager, "GET", "/properties/:id/water/invoices/:period", auth.InvoicesRead, app.showWaterInvoicesPDFHandler)
			guard(manager, "GET", "/properties/:id/garbage/generate", auth.ChargesPreview, app.previewGarbageChargesHandler)
			guard(manager, "POST", "/properties/:id/garbage/generate", auth.ChargesGenerate, app.generateGarbageChargesHandler)
			guard(manager, "GET", "/properties/:id/garbage/invoices/:period", auth.InvoicesRead, app.showGarbageInvoicesPDFHandler)
			guard(manager, "GET", "/units/:id/ledger/:type", auth.LedgerRead, app.showUnitLedgerHandler)
			guard(manager, "GET", "/properties/:id/reports/:period", auth.ReportsRead, app.showMonthlyReportHandler)
			guard(manager, "GET", "/properties/:id/reports/:period/plot-meter", auth.PlotMeterRead, app.showPlotMeterReadingHandler)
			guard(manager, "PUT", "/properties/:id/reports/:period/plot-meter", auth.PlotMeterWrite, app.updatePlotMeterReadingHandler)
			guard(manager, "GET", "/properties/:id/reports/:period/checks", auth.ReportsRead, app.showReportChecksHandler)
			guard(manager, "POST", "/properties/:id/reports/:period/confirm", auth.ReportsConfirm, app.confirmMonthlyReportHandler)
			guard(manager, "POST", "/properties/:id/reports/:period/generate", auth.ReportsConfirm, app.generateMonthlyReportHandler)
			guard(manager, "POST", "/units/:id/arrears-commitments", auth.ArrearsWrite, app.createArrearsCommitmentHandler)
			guard(manager, "GET", "/properties/:id/receipts/:period/download", auth.ReceiptsRead, app.showReceiptsPDFHandler)
		}

		// -- Tenant-facing public payment surface: no login, OTP-gated, and
		// rate limited throughout (system-design.txt 4.2, 4.7). --
		payPerIP, payPerIntent := app.payRateLimits()
		pay := v1.Group("/pay", payPerIP)
		{
			public(pay, "GET", "/:propertySlug/:unitCode", app.showPayPageHandler)
			public(pay, "POST", "/:propertySlug/:unitCode/otp", app.requestPayOTPHandler)
			public(pay, "POST", "/:propertySlug/:unitCode/otp/verify", app.verifyPayOTPHandler)
			public(pay, "GET", "/:propertySlug/:unitCode/balances", app.showPayBalancesHandler)
			public(pay, "POST", "/:propertySlug/:unitCode/intent", app.createPaymentIntentHandler)
			public(pay, "GET", "/intents/:id/status", payPerIntent, app.showPaymentIntentStatusHandler)
		}

		// -- PayHero webhooks: server to server, verified in the handlers,
		// and exempt from rate limiting (system-design.txt 4.2, 4.8). --
		public(v1, "POST", "/webhooks/payhero/collections", app.payheroCollectionsWebhookHandler)

		// -- Super Admin only (system-design.txt 4.5, 4.9) --
		admin := v1.Group("/admin", app.requireAdmin())
		{
			guard(admin, "GET", "/system/health", auth.AdminSystemRead, app.adminSystemHealthHandler)
			guard(admin, "GET", "/backups", auth.AdminSystemRead, app.adminListBackupsHandler)
		}
	}

	return router, nil
}
