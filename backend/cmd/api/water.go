package main

import (
	"net/http"

	"github.com/codercollo/willcoll/backend/internal/data"
	"github.com/codercollo/willcoll/backend/internal/pdf"
	"github.com/codercollo/willcoll/backend/internal/validator"
	"github.com/codercollo/willcoll/backend/pkg/moneyfmt"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const maxReadingsPerSave = 500

// showWaterReadingsHandler handles GET /v1/properties/:id/water?period=,
// the draft grid: one row per occupied unit with its reading, computed
// amount and prior water balance (system-design.txt 3.9).
func (app *application) showWaterReadingsHandler(c *gin.Context) {
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

	grid, err := app.models.Water.Grid(c.Request.Context(), tenantID, propertyID, period)
	if err != nil {
		app.billingErrorResponse(c, err)
		return
	}
	if err := app.writeJSON(c, http.StatusOK, envelope{"water": grid}, nil); err != nil {
		app.serverErrorResponse(c, err)
	}
}

// updateWaterReadingsHandler handles PUT /v1/properties/:id/water?period=,
// the bulk save of draft readings. All rows save or none do.
func (app *application) updateWaterReadingsHandler(c *gin.Context) {
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

	var input struct {
		Readings []struct {
			UnitID          uuid.UUID       `json:"unit_id"`
			CurrentReading  *moneyfmt.Money `json:"current_reading"`
			PreviousReading *moneyfmt.Money `json:"previous_reading"`
			Rate            *moneyfmt.Money `json:"rate"`
		} `json:"readings"`
	}
	if err := app.readJSON(c, &input); err != nil {
		app.badRequestResponse(c, err)
		return
	}

	v := validator.New()
	period := app.readPeriodQuery(c.Request.URL.Query(), v)
	v.Check(len(input.Readings) > 0, "readings", "must contain at least one reading")
	v.Check(len(input.Readings) <= maxReadingsPerSave, "readings", "must not contain more than 500 readings")
	seen := make(map[uuid.UUID]bool, len(input.Readings))
	for _, r := range input.Readings {
		v.Check(r.UnitID != uuid.Nil, "readings", "every reading needs a unit_id")
		v.Check(r.CurrentReading != nil, "readings", "every reading needs a current_reading")
		v.Check(!seen[r.UnitID], "readings", "each unit may appear only once")
		seen[r.UnitID] = true
	}
	if !v.Valid() {
		app.failedValidationResponse(c, v.Errors)
		return
	}

	inputs := make([]data.WaterReadingInput, len(input.Readings))
	for i, r := range input.Readings {
		inputs[i] = data.WaterReadingInput{
			UnitID: r.UnitID, CurrentReading: *r.CurrentReading,
			PreviousReading: r.PreviousReading, Rate: r.Rate,
		}
	}

	if err := app.models.Water.SaveReadings(c.Request.Context(), tenantID, manager.ID, propertyID, period, inputs); err != nil {
		app.billingErrorResponse(c, err)
		return
	}

	// Answer with the recomputed grid so the client shows the DB's numbers.
	grid, err := app.models.Water.Grid(c.Request.Context(), tenantID, propertyID, period)
	if err != nil {
		app.billingErrorResponse(c, err)
		return
	}
	if err := app.writeJSON(c, http.StatusOK, envelope{"water": grid}, nil); err != nil {
		app.serverErrorResponse(c, err)
	}
}

// generateWaterChargesHandler handles POST /v1/properties/:id/water/generate?period=:
// lock the readings and post the charges in one transaction. It is
// synchronous: the ledger write is a single transaction, and the bills are
// rendered on demand by showWaterInvoicesPDFHandler, so there is no
// background work to poll.
func (app *application) generateWaterChargesHandler(c *gin.Context) {
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

	result, err := app.models.Water.Generate(c.Request.Context(), tenantID, manager.ID, propertyID, period)
	if err != nil {
		app.billingErrorResponse(c, err)
		return
	}
	if err := app.writeJSON(c, http.StatusCreated, envelope{"run": result}, nil); err != nil {
		app.serverErrorResponse(c, err)
	}
}

// showWaterInvoicesPDFHandler handles GET /v1/properties/:id/water/invoices/:period
// (":period" is "YYYY-MM.pdf"). One PDF holds every billed unit's bill, a
// page each; ?unit_id= renders a single unit's.
func (app *application) showWaterInvoicesPDFHandler(c *gin.Context) {
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

	invoices, err := app.models.Water.Invoices(c.Request.Context(), tenantID, propertyID, period, unitID)
	if err != nil {
		app.billingErrorResponse(c, err)
		return
	}
	b, err := pdf.BuildWaterInvoices(invoices)
	if err != nil {
		app.serverErrorResponse(c, err)
		return
	}
	app.writePDF(c, "water-"+period.String()+".pdf", b)
}
