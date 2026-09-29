package main

import (
	"errors"
	"net/http"

	"github.com/codercollo/willcoll/backend/internal/data"
	"github.com/codercollo/willcoll/backend/internal/validator"
	"github.com/codercollo/willcoll/backend/pkg/moneyfmt"
	"github.com/gin-gonic/gin"
)

// updatePropertyGarbageHandler handles PATCH /v1/properties/:id/garbage,
// the dedicated toggle + fee endpoint for a property's garbage billing
// (system-design.txt 4.6). Both fields are pointers, so an omitted key
// leaves that setting unchanged (Greenlight partial update): sending only
// {"fee": "350.00"} changes the fee without switching billing on or off.
func (app *application) updatePropertyGarbageHandler(c *gin.Context) {
	tenantID, ok := contextGetTenantID(c)
	if !ok {
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
		Enabled *bool           `json:"enabled"`
		Fee     *moneyfmt.Money `json:"fee"`
	}

	if err := app.readJSON(c, &input); err != nil {
		app.badRequestResponse(c, err)
		return
	}

	if input.Enabled != nil {
		property.GarbageEnabled = *input.Enabled
	}
	if input.Fee != nil {
		property.GarbageFee = *input.Fee
	}

	v := validator.New()
	v.Struct(input)
	v.Check(!property.GarbageEnabled || property.GarbageFee.IsPositive(), "fee", "must be greater than zero while garbage billing is enabled")
	if data.ValidateProperty(v, property); !v.Valid() {
		app.failedValidationResponse(c, v.Errors)
		return
	}

	if err := app.models.Properties.Update(c.Request.Context(), property); err != nil {
		app.propertyWriteErrorResponse(c, err)
		return
	}

	if err := app.writeJSON(c, http.StatusOK, envelope{"property": property}, nil); err != nil {
		app.serverErrorResponse(c, err)
	}
}
