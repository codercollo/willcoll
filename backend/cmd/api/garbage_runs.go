package main

import (
	"net/http"

	"github.com/codercollo/willcoll/backend/internal/pdf"
	"github.com/codercollo/willcoll/backend/internal/validator"
	"github.com/gin-gonic/gin"
)

// previewGarbageChargesHandler handles GET /v1/properties/:id/garbage/generate?period=:
// who a garbage run would bill, at what fee, and whether the period is
// already billed.
func (app *application) previewGarbageChargesHandler(c *gin.Context) {
	tenantID, ok := contextGetTenantID(c)
	if !ok {
		app.authenticationRequiredResponse(c)
		return
	}
	propertyID, err := app.readIDParam(c)
	if err != nil {
		app.notFoundResponse(c)
		return
	}

	v := validator.New()
	period := app.readPeriodQuery(c.Request.URL.Query(), v)
	if !v.Valid() {
		app.failedValidationResponse(c, v.Errors)
		return
	}

	preview, err := app.models.Garbage.Preview(c.Request.Context(), tenantID, propertyID, period)
	if err != nil {
		app.billingErrorResponse(c, err)
		return
	}
	if err := app.writeJSON(c, http.StatusOK, envelope{"garbage": preview}, nil); err != nil {
		app.serverErrorResponse(c, err)
	}
}

// generateGarbageChargesHandler handles POST /v1/properties/:id/garbage/generate?period=:
// bill the fixed garbage fee to every occupied unit. Synchronous, like the
// water run: one ledger transaction, with the bills rendered on demand.
func (app *application) generateGarbageChargesHandler(c *gin.Context) {
	manager := contextGetManager(c)
	tenantID, ok := contextGetTenantID(c)
	if manager.IsAnonymous() || !ok {
		app.authenticationRequiredResponse(c)
		return
	}
	propertyID, err := app.readIDParam(c)
	if err != nil {
		app.notFoundResponse(c)
		return
	}

	v := validator.New()
	period := app.readPeriodQuery(c.Request.URL.Query(), v)
	if !v.Valid() {
		app.failedValidationResponse(c, v.Errors)
		return
	}

	result, err := app.models.Garbage.Generate(c.Request.Context(), tenantID, manager.ID, propertyID, period)
	if err != nil {
		app.billingErrorResponse(c, err)
		return
	}
	if err := app.writeJSON(c, http.StatusCreated, envelope{"run": result}, nil); err != nil {
		app.serverErrorResponse(c, err)
	}
}

// showGarbageInvoicesPDFHandler handles GET /v1/properties/:id/garbage/invoices/:period
// (":period" is "YYYY-MM.pdf"); ?unit_id= renders a single unit's bill.
func (app *application) showGarbageInvoicesPDFHandler(c *gin.Context) {
	tenantID, ok := contextGetTenantID(c)
	if !ok {
		app.authenticationRequiredResponse(c)
		return
	}
	propertyID, err := app.readIDParam(c)
	if err != nil {
		app.notFoundResponse(c)
		return
	}
	period, ok := readPeriod(c.Param("period"))
	if !ok {
		app.notFoundResponse(c)
		return
	}

	v := validator.New()
	unitID := app.readUUID(c.Request.URL.Query(), "unit_id", v)
	if !v.Valid() {
		app.failedValidationResponse(c, v.Errors)
		return
	}

	invoices, err := app.models.Garbage.Invoices(c.Request.Context(), tenantID, propertyID, period, unitID)
	if err != nil {
		app.billingErrorResponse(c, err)
		return
	}
	b, err := pdf.BuildGarbageInvoices(invoices)
	if err != nil {
		app.serverErrorResponse(c, err)
		return
	}
	app.writePDF(c, "garbage-"+period.String()+".pdf", b)
}
