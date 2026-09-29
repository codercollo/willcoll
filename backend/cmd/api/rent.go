package main

import (
	"net/http"
	"strings"
	"time"

	"github.com/codercollo/willcoll/backend/internal/data"
	"github.com/codercollo/willcoll/backend/internal/validator"
	"github.com/codercollo/willcoll/backend/pkg/moneyfmt"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// updateRentScheduleHandler handles PUT /v1/properties/:id/rent-schedule:
// the spreadsheet save of expected monthly rent per unit, as a JSON array
// [{"unit_id": ..., "rent_amount": "6000.00"}, ...]. All rows save or none
// do. It posts nothing; only later rent runs pick up the new amounts.
func (app *application) updateRentScheduleHandler(c *gin.Context) {
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

	var input []struct {
		UnitID     uuid.UUID       `json:"unit_id"`
		RentAmount *moneyfmt.Money `json:"rent_amount"`
	}
	if err := app.readJSON(c, &input); err != nil {
		app.badRequestResponse(c, err)
		return
	}

	v := validator.New()
	v.Check(len(input) > 0, "rent_schedule", "must contain at least one unit")
	v.Check(len(input) <= maxReadingsPerSave, "rent_schedule", "must not contain more than 500 units")
	seen := make(map[uuid.UUID]bool, len(input))
	for _, r := range input {
		v.Check(r.UnitID != uuid.Nil, "rent_schedule", "every row needs a unit_id")
		v.Check(r.RentAmount != nil, "rent_schedule", "every row needs a rent_amount")
		v.Check(!seen[r.UnitID], "rent_schedule", "each unit may appear only once")
		seen[r.UnitID] = true
	}
	if !v.Valid() {
		app.failedValidationResponse(c, v.Errors)
		return
	}

	inputs := make([]data.UnitRentInput, len(input))
	for i, r := range input {
		inputs[i] = data.UnitRentInput{UnitID: r.UnitID, RentAmount: *r.RentAmount}
	}
	if err := app.models.Rent.BulkSetExpectedRent(c.Request.Context(), tenantID, propertyID, inputs); err != nil {
		app.billingErrorResponse(c, err)
		return
	}

	overview, err := app.models.Rent.GetPropertyOverview(c.Request.Context(), tenantID, propertyID, moneyfmt.NewPeriod(time.Now()))
	if err != nil {
		app.billingErrorResponse(c, err)
		return
	}
	if err := app.writeJSON(c, http.StatusOK, envelope{"rent": overview}, nil); err != nil {
		app.serverErrorResponse(c, err)
	}
}

// generateRentChargesHandler handles POST /v1/properties/:id/rent/generate?period=YYYY-MM-01:
// bill the month's rent. Re-running a period is safe: billed units are
// skipped and reported in the summary.
func (app *application) generateRentChargesHandler(c *gin.Context) {
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

	summary, err := app.models.Rent.GenerateForPeriod(c.Request.Context(), tenantID, manager.ID, propertyID, period)
	if err != nil {
		app.billingErrorResponse(c, err)
		return
	}
	if err := app.writeJSON(c, http.StatusCreated, envelope{"run": summary}, nil); err != nil {
		app.serverErrorResponse(c, err)
	}
}

// showRentOverviewHandler handles GET /v1/properties/:id/rent/overview?period=:
// the Rent tab, every occupied unit's expected, paid this period and
// balance, with a status (paid, partial or arrears). The period defaults to
// the current month.
func (app *application) showRentOverviewHandler(c *gin.Context) {
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

	period := moneyfmt.NewPeriod(time.Now())
	if raw := c.Request.URL.Query().Get("period"); raw != "" {
		v := validator.New()
		period = app.readPeriodQuery(c.Request.URL.Query(), v)
		if !v.Valid() {
			app.failedValidationResponse(c, v.Errors)
			return
		}
	}

	rows, err := app.models.Rent.GetPropertyOverview(c.Request.Context(), tenantID, propertyID, period)
	if err != nil {
		app.billingErrorResponse(c, err)
		return
	}
	if err := app.writeJSON(c, http.StatusOK, envelope{"period": period, "overview": rows}, nil); err != nil {
		app.serverErrorResponse(c, err)
	}
}

// createRentPaymentHandler handles POST /v1/units/:id/rent/payments: a
// payment the manager received outside PayHero (cash or bank). Body:
// {"amount", "source": "manual"|"bank", "reference", "note", "ledger_type"}
// where ledger_type defaults to rent.
func (app *application) createRentPaymentHandler(c *gin.Context) {
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
		Amount     *moneyfmt.Money `json:"amount"`
		Source     string          `json:"source"`
		Reference  string          `json:"reference"`
		Note       string          `json:"note"`
		LedgerType string          `json:"ledger_type"`
	}
	if err := app.readJSON(c, &input); err != nil {
		app.badRequestResponse(c, err)
		return
	}

	v := validator.New()
	v.Check(input.Amount != nil, "amount", "must be provided")
	v.Check(validator.PermittedValue(input.Source, "manual", "bank"), "source", "must be manual or bank")
	ledgerType := ledgerTypeOrDefault(input.LedgerType)
	v.Check(validator.PermittedValue(ledgerType, ledgerTypeSafelist...), "ledger_type", "must be one of rent, water, garbage, rent_deposit, water_deposit, electricity_deposit")
	if !v.Valid() {
		app.failedValidationResponse(c, v.Errors)
		return
	}

	payment, err := app.models.Rent.PostPayment(c.Request.Context(), tenantID, manager.ID, unitID, data.PaymentInput{
		Amount: *input.Amount, Source: input.Source, Reference: input.Reference, Note: input.Note, LedgerType: ledgerType,
	})
	if err != nil {
		app.billingErrorResponse(c, err)
		return
	}
	if err := app.writeJSON(c, http.StatusCreated, envelope{"payment": payment}, nil); err != nil {
		app.serverErrorResponse(c, err)
	}
}

// createRentChargeHandler handles POST /v1/units/:id/rent/charges: an ad-hoc
// charge (a penalty, or the corrected entry after a reversal), posted as a
// MANUAL_ADJUSTMENT. Body: {"amount", "description", "ledger_type"}.
func (app *application) createRentChargeHandler(c *gin.Context) {
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
		Amount      *moneyfmt.Money `json:"amount"`
		Description string          `json:"description"`
		LedgerType  string          `json:"ledger_type"`
	}
	if err := app.readJSON(c, &input); err != nil {
		app.badRequestResponse(c, err)
		return
	}

	v := validator.New()
	v.Check(input.Amount != nil, "amount", "must be provided")
	v.Check(input.Description != "", "description", "must be provided")
	ledgerType := ledgerTypeOrDefault(input.LedgerType)
	v.Check(validator.PermittedValue(ledgerType, ledgerTypeSafelist...), "ledger_type", "must be one of rent, water, garbage, rent_deposit, water_deposit, electricity_deposit")
	if !v.Valid() {
		app.failedValidationResponse(c, v.Errors)
		return
	}

	if err := app.models.Rent.PostManualCharge(c.Request.Context(), tenantID, manager.ID, unitID, ledgerType, *input.Amount, input.Description); err != nil {
		app.billingErrorResponse(c, err)
		return
	}
	if err := app.writeJSON(c, http.StatusCreated, envelope{"message": "charge posted"}, nil); err != nil {
		app.serverErrorResponse(c, err)
	}
}

// reverseLedgerEntryHandler handles POST /v1/ledger-entries/:id/reverse.
// Body: {"reason": "..."}. It never edits the entry: a reversal is appended
// and both stay in the ledger. It works on any ledger's entries (rent, water,
// garbage, deposits); a payment reversal also removes the payment from
// reports.
func (app *application) reverseLedgerEntryHandler(c *gin.Context) {
	manager := contextGetManager(c)
	tenantID, ok := contextGetTenantID(c)
	if manager.IsAnonymous() || !ok {
		app.authenticationRequiredResponse(c)
		return
	}
	entryID, err := app.readIDParam(c)
	if err != nil {
		app.notFoundResponse(c)
		return
	}

	var input struct {
		Reason string `json:"reason"`
	}
	if err := app.readJSON(c, &input); err != nil {
		app.badRequestResponse(c, err)
		return
	}
	v := validator.New()
	v.Check(input.Reason != "", "reason", "must be provided")
	if !v.Valid() {
		app.failedValidationResponse(c, v.Errors)
		return
	}

	reversalID, err := app.models.Rent.ReverseEntry(c.Request.Context(), tenantID, manager.ID, entryID, input.Reason)
	if err != nil {
		app.billingErrorResponse(c, err)
		return
	}
	if err := app.writeJSON(c, http.StatusCreated, envelope{"reversal_entry_id": reversalID}, nil); err != nil {
		app.serverErrorResponse(c, err)
	}
}

// ledgerTypeOrDefault upper-cases the lowercase API spelling and defaults to
// RENT.
func ledgerTypeOrDefault(s string) string {
	if s == "" {
		return data.LedgerTypeRent
	}
	return strings.ToUpper(s)
}
