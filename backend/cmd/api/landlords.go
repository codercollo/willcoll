package main

import (
	"errors"
	"net/http"

	"github.com/codercollo/willcoll/backend/internal/data"
	"github.com/codercollo/willcoll/backend/internal/validator"
	"github.com/gin-gonic/gin"
)

// createLandlordHandler handles POST /v1/landlords.
func (app *application) createLandlordHandler(c *gin.Context) {
	tenantID, ok := contextGetTenantID(c)
	if !ok {
		app.authenticationRequiredResponse(c)
		return
	}

	var input struct {
		Name              string  `json:"name" validate:"required,max=500"`
		Phone             string  `json:"phone" validate:"required,phone"`
		Email             *string `json:"email" validate:"omitnil,email"`
		BankName          *string `json:"bank_name"`
		BankAccountName   *string `json:"bank_account_name"`
		BankAccountNumber *string `json:"bank_account_number"`
	}

	if err := app.readJSON(c, &input); err != nil {
		app.badRequestResponse(c, err)
		return
	}

	landlord := &data.Landlord{
		Name:              input.Name,
		Phone:             input.Phone,
		Email:             input.Email,
		BankName:          input.BankName,
		BankAccountName:   input.BankAccountName,
		BankAccountNumber: input.BankAccountNumber,
	}

	v := validator.New()
	v.Struct(input)
	if data.ValidateLandlord(v, landlord); !v.Valid() {
		app.failedValidationResponse(c, v.Errors)
		return
	}

	if err := app.models.Landlords.Insert(c.Request.Context(), tenantID, landlord); err != nil {
		app.serverErrorResponse(c, err)
		return
	}

	if err := app.writeJSON(c, http.StatusCreated, envelope{"landlord": landlord}, nil); err != nil {
		app.serverErrorResponse(c, err)
	}
}

// listLandlordsHandler handles GET /v1/landlords.
func (app *application) listLandlordsHandler(c *gin.Context) {
	tenantID, ok := contextGetTenantID(c)
	if !ok {
		app.authenticationRequiredResponse(c)
		return
	}

	var input struct {
		Filters data.Filters
	}

	qs := c.Request.URL.Query()
	v := validator.New()

	input.Filters.Page = app.readInt(qs, "page", 1, v)
	input.Filters.PageSize = app.readInt(qs, "page_size", 20, v)
	input.Filters.Sort = app.readString(qs, "sort", "name")
	input.Filters.SortSafelist = []string{"name", "-name", "created_at", "-created_at"}

	if data.ValidateFilters(v, input.Filters); !v.Valid() {
		app.failedValidationResponse(c, v.Errors)
		return
	}

	landlords, metadata, err := app.models.Landlords.GetAll(c.Request.Context(), tenantID, input.Filters)
	if err != nil {
		app.serverErrorResponse(c, err)
		return
	}

	err = app.writeJSON(c, http.StatusOK, envelope{"landlords": landlords, "metadata": metadata}, nil)
	if err != nil {
		app.serverErrorResponse(c, err)
	}
}

// showLandlordHandler handles GET /v1/landlords/:id.
func (app *application) showLandlordHandler(c *gin.Context) {
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

	landlord, err := app.models.Landlords.Get(c.Request.Context(), tenantID, id)
	if err != nil {
		switch {
		case errors.Is(err, data.ErrRecordNotFound):
			app.notFoundResponse(c)
		default:
			app.serverErrorResponse(c, err)
		}
		return
	}

	if err := app.writeJSON(c, http.StatusOK, envelope{"landlord": landlord}, nil); err != nil {
		app.serverErrorResponse(c, err)
	}
}

// updateLandlordHandler handles PATCH /v1/landlords/:id. Every field is a
// pointer so an absent JSON key leaves the existing value untouched
// (build-order Stage 4.3 — partial updates).
func (app *application) updateLandlordHandler(c *gin.Context) {
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

	landlord, err := app.models.Landlords.Get(c.Request.Context(), tenantID, id)
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
		Name              *string `json:"name" validate:"omitnil,min=1,max=500"`
		Phone             *string `json:"phone" validate:"omitnil,phone"`
		Email             *string `json:"email" validate:"omitnil,email"`
		BankName          *string `json:"bank_name"`
		BankAccountName   *string `json:"bank_account_name"`
		BankAccountNumber *string `json:"bank_account_number"`
	}

	if err := app.readJSON(c, &input); err != nil {
		app.badRequestResponse(c, err)
		return
	}

	if input.Name != nil {
		landlord.Name = *input.Name
	}
	if input.Phone != nil {
		landlord.Phone = *input.Phone
	}
	if input.Email != nil {
		landlord.Email = input.Email
	}
	if input.BankName != nil {
		landlord.BankName = input.BankName
	}
	if input.BankAccountName != nil {
		landlord.BankAccountName = input.BankAccountName
	}
	if input.BankAccountNumber != nil {
		landlord.BankAccountNumber = input.BankAccountNumber
	}

	v := validator.New()
	v.Struct(input)
	if data.ValidateLandlord(v, landlord); !v.Valid() {
		app.failedValidationResponse(c, v.Errors)
		return
	}

	if err := app.models.Landlords.Update(c.Request.Context(), landlord); err != nil {
		switch {
		case errors.Is(err, data.ErrEditConflict):
			app.editConflictResponse(c)
		default:
			app.serverErrorResponse(c, err)
		}
		return
	}

	if err := app.writeJSON(c, http.StatusOK, envelope{"landlord": landlord}, nil); err != nil {
		app.serverErrorResponse(c, err)
	}
}
