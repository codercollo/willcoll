package main

import (
	"expvar"

	"github.com/gin-gonic/gin"
)

// routes wires the full v1 API surface, grouped by the system-design.txt
// section that specifies each group. Every route has a working handler: none
// is a placeholder.
func (app *application) routes() (*gin.Engine, error) {
	router := gin.New()

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

	// expvar metrics. Caddy only exposes this to localhost/VPN
	// (system-design.txt 4.2); it is not part of the public API.
	router.GET("/debug/vars", gin.WrapH(expvar.Handler()))

	// One limiter shared by every rate-limited route, so a client can't
	// multiply its budget by spreading requests across them.
	rateLimited := app.rateLimit()

	// Public QR scan, outside /v1: the branded domain's /q/* is routed here by
	// the reverse proxy. Rate limited like the pay surface.
	router.GET("/q/:token", rateLimited, app.scanUnitQRHandler)

	v1 := router.Group("/v1")
	{
		v1.GET("/healthcheck", app.healthcheckHandler)

		// -- Manager dashboard: an activated manager's session, and every
		// query inside that manager's RLS scope (system-design.txt 4.5).
		// requireModule/requirePlatformSubscription read the module
		// entitlement and trial/subscription from willcoll_platform and
		// always enforce (build-phases.txt 3i-b).
		// requireStaffModuleAccess/requireRole (3f-2) are no-ops for the
		// manager's own login (always the implicit owner) and only
		// matter once a staff member signs in. --
		manager := v1.Group("", app.requireActivatedManager(), app.requireStaffModuleAccess("rentals"),
			app.requireModule("rentals"), app.requirePlatformSubscription("rentals"),
			app.requireRole())
		{
			// Core resources (system-design.txt 4.6)
			manager.GET("/landlords", app.listLandlordsHandler)
			manager.POST("/landlords", app.createLandlordHandler)
			manager.GET("/landlords/:id", app.showLandlordHandler)
			manager.PATCH("/landlords/:id", app.updateLandlordHandler)

			manager.GET("/properties", app.listPropertiesHandler)
			manager.POST("/properties", app.createPropertyHandler)
			manager.GET("/properties/:id", app.showPropertyHandler)
			manager.PATCH("/properties/:id", app.updatePropertyHandler)
			manager.DELETE("/properties/:id", app.deletePropertyHandler) // soft delete (archive)
			manager.PATCH("/properties/:id/garbage", app.updatePropertyGarbageHandler)
			manager.PATCH("/properties/:id/electricity", app.updatePropertyElectricityHandler)
			manager.GET("/properties/:id/electricity/deposits", app.showElectricityDepositsHandler)
			manager.GET("/properties/:id/electricity/deposits.csv", app.showElectricityDepositsCSVHandler)
			manager.GET("/properties/:id/electricity/deposits.pdf", app.showElectricityDepositsPDFHandler)
			manager.PUT("/properties/:id/print-theme", app.updatePropertyPrintThemeHandler)
			manager.POST("/properties/:id/payment-channel", app.registerPaymentChannelHandler)
			manager.GET("/properties/:id/payment-channel/status", app.showPaymentChannelStatusHandler)
			manager.POST("/properties/:id/payment-channel/test", app.sendPaymentChannelTestHandler)
			manager.GET("/payment-channel-banks", app.listPaymentChannelBanksHandler)

			manager.GET("/properties/:id/units", app.listUnitsForPropertyHandler)
			manager.POST("/properties/:id/units", app.createUnitHandler)
			manager.POST("/properties/:id/units/import", app.importUnitsHandler)
			manager.GET("/onboarding", app.showOnboardingHandler)
			manager.POST("/properties/:id/onboarding/import", app.onboardingImportHandler) // units + tenants + opening balances
			manager.GET("/units/:id", app.showUnitHandler)
			manager.PATCH("/units/:id", app.updateUnitHandler)

			manager.POST("/units/:id/leases", app.createLeaseHandler)
			manager.GET("/units/:id/leases", app.listUnitLeasesHandler)
			manager.POST("/leases/:id/payers", app.addLeasePayerHandler)
			manager.DELETE("/leases/:id/payers/:payerId", app.removeLeasePayerHandler)
			manager.GET("/leases/:id", app.showLeaseHandler)
			manager.PATCH("/leases/:id", app.updateLeaseHandler)
			// Lease-bound unit QR codes: the sticker on the door.
			manager.GET("/leases/:id/qr", app.showUnitQRHandler)
			manager.GET("/leases/:id/qr.png", app.showUnitQRImageHandler)
			manager.POST("/leases/:id/qr/rotate", app.rotateUnitQRHandler)
			manager.POST("/properties/:id/qr/bulk", app.bulkGenerateQRHandler)

			// Billing engines, ledgers, reports (system-design.txt 4.6)
			manager.PUT("/properties/:id/rent-schedule", app.updateRentScheduleHandler)
			manager.POST("/properties/:id/rent/generate", app.generateRentChargesHandler)
			manager.GET("/properties/:id/rent/overview", app.showRentOverviewHandler)
			manager.POST("/units/:id/rent/payments", app.createRentPaymentHandler)
			manager.POST("/units/:id/rent/charges", app.createRentChargeHandler)
			manager.POST("/ledger-entries/:id/reverse", app.reverseLedgerEntryHandler)

			// Payment review queue (system-design.txt 4.8, 5)
			manager.GET("/payments/review", app.listPaymentsForReviewHandler)
			manager.POST("/payments/:id/allocate", app.allocatePaymentHandler)
			manager.POST("/payments/:id/confirm", app.confirmPaymentHandler)

			manager.GET("/properties/:id/water", app.showWaterReadingsHandler)
			manager.PUT("/properties/:id/water", app.updateWaterReadingsHandler)
			manager.POST("/properties/:id/water/generate", app.generateWaterChargesHandler)
			// Gin params can't carry a literal suffix, so :period arrives as
			// "<period>.pdf" and the handler strips the extension.
			manager.GET("/properties/:id/water/invoices/:period", app.showWaterInvoicesPDFHandler)
			manager.GET("/properties/:id/garbage/generate", app.previewGarbageChargesHandler)
			manager.POST("/properties/:id/garbage/generate", app.generateGarbageChargesHandler)
			manager.GET("/properties/:id/garbage/invoices/:period", app.showGarbageInvoicesPDFHandler)
			manager.GET("/units/:id/ledger/:type", app.showUnitLedgerHandler)
			manager.GET("/properties/:id/reports/:period", app.showMonthlyReportHandler)
			manager.GET("/properties/:id/reports/:period/plot-meter", app.showPlotMeterReadingHandler)
			manager.PUT("/properties/:id/reports/:period/plot-meter", app.updatePlotMeterReadingHandler)
			manager.GET("/properties/:id/reports/:period/checks", app.showReportChecksHandler)
			manager.POST("/properties/:id/reports/:period/confirm", app.confirmMonthlyReportHandler)
			manager.POST("/properties/:id/reports/:period/generate", app.generateMonthlyReportHandler)
			manager.POST("/units/:id/arrears-commitments", app.createArrearsCommitmentHandler)
			manager.GET("/properties/:id/receipts/:period/download", app.showReceiptsPDFHandler)
		}

		// -- Tenant-facing public payment surface: no login, OTP-gated, and
		// rate limited throughout (system-design.txt 4.2, 4.7). --
		pay := v1.Group("/pay", rateLimited)
		{
			pay.GET("/:propertySlug/:unitCode", app.showPayPageHandler)
			pay.POST("/:propertySlug/:unitCode/otp", app.requestPayOTPHandler)
			pay.POST("/:propertySlug/:unitCode/otp/verify", app.verifyPayOTPHandler)
			pay.GET("/:propertySlug/:unitCode/balances", app.showPayBalancesHandler)
			pay.POST("/:propertySlug/:unitCode/intent", app.createPaymentIntentHandler)
			pay.GET("/intents/:id/status", app.showPaymentIntentStatusHandler)
		}

		// -- PayHero webhooks: server to server, verified in the handlers,
		// and exempt from rate limiting (system-design.txt 4.2, 4.8). --
		v1.POST("/webhooks/payhero/collections", app.payheroCollectionsWebhookHandler)

		// -- Super Admin only (system-design.txt 4.5, 4.9) --
		admin := v1.Group("/admin", app.requireAdmin())
		{
			admin.GET("/system/health", app.adminSystemHealthHandler)
			admin.GET("/backups", app.adminListBackupsHandler)
		}
	}

	return router, nil
}
