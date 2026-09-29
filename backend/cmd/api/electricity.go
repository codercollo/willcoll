package main

import (
	"errors"
	"net/http"

	"github.com/codercollo/willcoll/backend/internal/data"
	"github.com/codercollo/willcoll/backend/internal/validator"
	"github.com/codercollo/willcoll/backend/pkg/moneyfmt"
	"github.com/gin-gonic/gin"
)

// updatePropertyElectricityHandler handles PATCH /v1/properties/:id/electricity,
// the dedicated toggle + default-deposit-amount endpoint for a property's
// electricity deposit — the same shape as updatePropertyGarbageHandler.
// Both fields are pointers, so an omitted key leaves that setting unchanged.
// Turning the toggle on (false -> true) also retroactively charges the
// deposit to every active lease that doesn't have one yet
// (data.LeaseModel.EnableElectricityDeposit); a lease created afterward is
// charged at lease start like any other deposit.
func (app *application) updatePropertyElectricityHandler(c *gin.Context) {
	manager := contextGetManager(c)
	tenantID, ok := contextGetTenantID(c)
	if manager.IsAnonymous() || !ok {
		app.authenticationRequiredResponse(c)
		return
	}

	id, err := app.readIDParam(c)
	if err != nil {
		app.notFoundResponse(c)
		return
	}

	property, err := app.models.Properties.Get(c.Request.Context(), tenantID, id)
	if err != nil {
		switch {
		case errors.Is(err, data.ErrRecordNotFound):
			app.notFoundResponse(c)
		default:
			app.serverErrorResponse(c, err)
		}
		return
	}

	var input struct {
		Enabled       *bool           `json:"enabled"`
		DepositAmount *moneyfmt.Money `json:"deposit_amount"`
	}

	if err := app.readJSON(c, &input); err != nil {
		app.badRequestResponse(c, err)
		return
	}

	wasEnabled := property.ElectricityEnabled
	if input.Enabled != nil {
		property.ElectricityEnabled = *input.Enabled
	}
	if input.DepositAmount != nil {
		property.ElectricityDepositAmount = *input.DepositAmount
	}

	v := validator.New()
	v.Struct(input)
	v.Check(!property.ElectricityEnabled || property.ElectricityDepositAmount.IsPositive(), "deposit_amount", "must be greater than zero while electricity is enabled")
	if data.ValidateProperty(v, property); !v.Valid() {
		app.failedValidationResponse(c, v.Errors)
		return
	}

	if err := app.models.Properties.Update(c.Request.Context(), property); err != nil {
		app.propertyWriteErrorResponse(c, err)
		return
	}

	var charged int
	if !wasEnabled && property.ElectricityEnabled {
		charged, err = app.models.Leases.EnableElectricityDeposit(c.Request.Context(), tenantID, manager.ID, property.ID, property.ElectricityDepositAmount)
		if err != nil {
			app.serverErrorResponse(c, err)
			return
		}
	}

	if err := app.writeJSON(c, http.StatusOK, envelope{"property": property, "leases_charged": charged}, nil); err != nil {
		app.serverErrorResponse(c, err)
	}
}
