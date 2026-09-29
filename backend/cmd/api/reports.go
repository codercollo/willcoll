package main

import (
	"net/http"
	"time"

	"github.com/codercollo/willcoll/backend/internal/pdf"
	"github.com/codercollo/willcoll/backend/internal/validator"
	"github.com/codercollo/willcoll/backend/pkg/moneyfmt"
	"github.com/gin-gonic/gin"
)

// showMonthlyReportHandler handles GET /v1/properties/:id/reports/:period,
// the totals preview of the "ALL IN ONE PAYMENTS SCHEDULE" (JSON).
func (app *application) showMonthlyReportHandler(c *gin.Context) {
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

	report, err := app.models.Reports.Preview(c.Request.Context(), tenantID, propertyID, period)
	if err != nil {
		app.billingErrorResponse(c, err)
		return
	}
	if err := app.writeJSON(c, http.StatusOK, envelope{"report": report}, nil); err != nil {
		app.serverErrorResponse(c, err)
	}
}

// showPlotMeterReadingHandler handles GET /v1/properties/:id/reports/:period/plot-meter:
// the reading form's state, "Previous: X  Current: [ ]  = Y units used".
func (app *application) showPlotMeterReadingHandler(c *gin.Context) {
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

	reading, err := app.models.Reports.PlotMeterGrid(c.Request.Context(), tenantID, propertyID, period)
	if err != nil {
		app.billingErrorResponse(c, err)
		return
	}
	if err := app.writeJSON(c, http.StatusOK, envelope{"plot_meter": reading}, nil); err != nil {
		app.serverErrorResponse(c, err)
	}
}

// updatePlotMeterReadingHandler handles PUT /v1/properties/:id/reports/:period/plot-meter:
// the reading on the plot's main meter dial for the period, for NOTE:1's
// storage-deviation line. Stored the same way as a unit's water reading
// (previous + current, units derived): previous_reading defaults to last
// period's saved current reading, editable only when there is none to
// default from. Omitting this call entirely simply omits the NOTE:1 lines.
func (app *application) updatePlotMeterReadingHandler(c *gin.Context) {
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
	period, ok := readPeriod(c.Param("period"))
	if !ok {
		app.notFoundResponse(c)
		return
	}

	var input struct {
		CurrentReading  *moneyfmt.Money `json:"current_reading"`
		PreviousReading *moneyfmt.Money `json:"previous_reading"`
		ReadingDate     *time.Time      `json:"reading_date"`
	}
	if err := app.readJSON(c, &input); err != nil {
		app.badRequestResponse(c, err)
		return
	}
	v := validator.New()
	v.Check(input.CurrentReading != nil, "current_reading", "must be a decimal number")
	if !v.Valid() {
		app.failedValidationResponse(c, v.Errors)
		return
	}

	var readingDate time.Time
	if input.ReadingDate != nil {
		readingDate = *input.ReadingDate
	}
	if err := app.models.Reports.SetPlotMeterReading(c.Request.Context(), tenantID, manager.ID, propertyID, period, *input.CurrentReading, input.PreviousReading, readingDate); err != nil {
		app.billingErrorResponse(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// showReportChecksHandler handles GET /v1/properties/:id/reports/:period/checks:
// the sanity-check panel behind the Confirm button. Never confirms anything.
func (app *application) showReportChecksHandler(c *gin.Context) {
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

	result, report, err := app.models.Reports.Checks(c.Request.Context(), tenantID, propertyID, period)
	if err != nil {
		app.billingErrorResponse(c, err)
		return
	}
	if err := app.writeJSON(c, http.StatusOK, envelope{"checks": result, "report": report}, nil); err != nil {
		app.serverErrorResponse(c, err)
	}
}

// confirmMonthlyReportHandler handles POST /v1/properties/:id/reports/:period/confirm:
// runs the sanity checks and, on success, freezes the period's totals and
// NOTE:1/NOTE:2 lines. 422 with the failing checks if any block; the PDF
// endpoints stay closed until this succeeds.
func (app *application) confirmMonthlyReportHandler(c *gin.Context) {
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
	period, ok := readPeriod(c.Param("period"))
	if !ok {
		app.notFoundResponse(c)
		return
	}

	report, err := app.models.Reports.Confirm(c.Request.Context(), tenantID, manager.ID, propertyID, period)
	if err != nil {
		app.billingErrorResponse(c, err)
		return
	}
	if err := app.writeJSON(c, http.StatusOK, envelope{"report": report}, nil); err != nil {
		app.serverErrorResponse(c, err)
	}
}

// createArrearsCommitmentHandler handles POST /v1/units/:id/arrears-commitments:
// the one manual input NOTE:2 still takes — a manager's record of a real
// commitment letter, not derivable from the ledger.
func (app *application) createArrearsCommitmentHandler(c *gin.Context) {
	manager := contextGetManager(c)
	tenantID, ok := contextGetTenantID(c)
	if manager.IsAnonymous() || !ok {
		app.authenticationRequiredResponse(c)
		return
	}
	unitID, err := app.readIDParam(c)
	if err != nil {
		app.notFoundResponse(c)
		return
	}

	var input struct {
		Period string `json:"period"`
		Note   string `json:"note"`
	}
	if err := app.readJSON(c, &input); err != nil {
		app.badRequestResponse(c, err)
		return
	}
	period, ok := readPeriod(input.Period)
	if !ok {
		app.errorResponse(c, http.StatusUnprocessableEntity, envelope{"period": "must be a month in the format YYYY-MM"})
		return
	}

	if err := app.models.Reports.CreateArrearsCommitment(c.Request.Context(), tenantID, manager.ID, unitID, period, input.Note); err != nil {
		app.billingErrorResponse(c, err)
		return
	}
	c.Status(http.StatusCreated)
}

// generateMonthlyReportHandler handles POST /v1/properties/:id/reports/:period/generate:
// streams the schedule PDF rendered from the period's confirmation
// snapshot. 409 if the period was never confirmed or has gone stale since
// (system-design.txt PDF confirmation addendum: nothing in a PDF is typed
// in by hand, and nothing renders from an unconfirmed or stale ledger view).
func (app *application) generateMonthlyReportHandler(c *gin.Context) {
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
	period, ok := readPeriod(c.Param("period"))
	if !ok {
		app.notFoundResponse(c)
		return
	}

	report, err := app.models.Reports.GenerateConfirmed(c.Request.Context(), tenantID, propertyID, period)
	if err != nil {
		app.billingErrorResponse(c, err)
		return
	}
	b, err := pdf.BuildMonthlySchedule(report.Schedule())
	if err != nil {
		app.serverErrorResponse(c, err)
		return
	}
	app.writePDF(c, "payments-schedule-"+period.String()+".pdf", b)
}

// showReceiptsPDFHandler handles GET /v1/properties/:id/receipts/:period/download:
// a bundle of the receipts already issued (at posting time, see issueReceipt
// in internal/data/allocation.go) for the period and property (?unit_id=
// for just one), streamed as a single PDF. A read, not a write — nothing is
// generated here.
func (app *application) showReceiptsPDFHandler(c *gin.Context) {
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

	receipts, err := app.models.Receipts.Generate(c.Request.Context(), tenantID, propertyID, period, unitID)
	if err != nil {
		app.billingErrorResponse(c, err)
		return
	}
	b, err := pdf.BuildReceipts(receipts)
	if err != nil {
		app.serverErrorResponse(c, err)
		return
	}
	app.writePDF(c, "receipts-"+period.String()+".pdf", b)
}
