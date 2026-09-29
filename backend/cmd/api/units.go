package main

import (
	"errors"
	"net/http"

	"github.com/codercollo/willcoll/backend/internal/data"
	"github.com/codercollo/willcoll/backend/internal/validator"
	"github.com/gin-gonic/gin"
)

// createUnitHandler handles POST /v1/properties/:id/units.
func (app *application) createUnitHandler(c *gin.Context) {
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

	var input struct {
		UnitCode    string  `json:"unit_code" validate:"required,max=50"`
		MeterNumber *string `json:"meter_number"`
	}

	if err := app.readJSON(c, &input); err != nil {
		app.badRequestResponse(c, err)
		return
	}

	unit := &data.Unit{
		PropertyID:  propertyID,
		UnitCode:    input.UnitCode,
		MeterNumber: input.MeterNumber,
		Status:      data.UnitStatusVacant,
	}

	v := validator.New()
	v.Struct(input)
	if data.ValidateUnit(v, unit); !v.Valid() {
		app.failedValidationResponse(c, v.Errors)
		return
	}

	if err := app.models.Units.Insert(c.Request.Context(), tenantID, unit); err != nil {
		app.unitWriteErrorResponse(c, err)
		return
	}

	if err := app.writeJSON(c, http.StatusCreated, envelope{"unit": unit}, nil); err != nil {
		app.serverErrorResponse(c, err)
	}
}

// listUnitsForPropertyHandler handles GET /v1/properties/:id/units — the
// Units tab. Query parameters: status (vacant|occupied), search (tenant or
// co-payer name, prefix-matched word by word), sort (unit_code|created_at,
// "-" for descending), page, page_size.
func (app *application) listUnitsForPropertyHandler(c *gin.Context) {
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

	var filters data.Filters

	qs := c.Request.URL.Query()
	v := validator.New()

	filter := data.UnitListFilter{
		Status:       app.readString(qs, "status", ""),
		TenantSearch: app.readString(qs, "search", ""),
	}
	if filter.Status != "" {
		v.Check(validator.PermittedValue(filter.Status, data.UnitStatusVacant, data.UnitStatusOccupied),
			"status", "must be vacant or occupied")
	}

	filters.Page = app.readInt(qs, "page", 1, v)
	filters.PageSize = app.readInt(qs, "page_size", 20, v)
	filters.Sort = app.readString(qs, "sort", "unit_code")
	filters.SortSafelist = []string{"unit_code", "-unit_code", "created_at", "-created_at"}

	if data.ValidateFilters(v, filters); !v.Valid() {
		app.failedValidationResponse(c, v.Errors)
		return
	}

	units, metadata, err := app.models.Units.GetAllForProperty(c.Request.Context(), tenantID, propertyID, filter, filters)
	if err != nil {
		switch {
		case errors.Is(err, data.ErrPropertyNotFound):
			app.notFoundResponse(c)
		default:
			app.serverErrorResponse(c, err)
		}
		return
	}

	err = app.writeJSON(c, http.StatusOK, envelope{"units": units, "metadata": metadata}, nil)
	if err != nil {
		app.serverErrorResponse(c, err)
	}
}

// showUnitHandler handles GET /v1/units/:id.
func (app *application) showUnitHandler(c *gin.Context) {
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

	unit, err := app.models.Units.Get(c.Request.Context(), tenantID, id)
	if err != nil {
		switch {
		case errors.Is(err, data.ErrRecordNotFound):
			app.notFoundResponse(c)
		default:
			app.serverErrorResponse(c, err)
		}
		return
	}

	if err := app.writeJSON(c, http.StatusOK, envelope{"unit": unit}, nil); err != nil {
		app.serverErrorResponse(c, err)
	}
}

// updateUnitHandler handles PATCH /v1/units/:id: unit_code and
// meter_number only. A unit's status follows its lease (occupied while a
// lease is active, vacant once terminated) and can't be set by hand, so a
// "status" key is rejected as unknown.
func (app *application) updateUnitHandler(c *gin.Context) {
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

	unit, err := app.models.Units.Get(c.Request.Context(), tenantID, id)
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
		UnitCode    *string `json:"unit_code" validate:"omitnil,min=1,max=50"`
		MeterNumber *string `json:"meter_number"`
	}

	if err := app.readJSON(c, &input); err != nil {
		app.badRequestResponse(c, err)
		return
	}

	if input.UnitCode != nil {
		unit.UnitCode = *input.UnitCode
	}
	if input.MeterNumber != nil {
		unit.MeterNumber = input.MeterNumber
	}

	v := validator.New()
	v.Struct(input)
	if data.ValidateUnit(v, unit); !v.Valid() {
		app.failedValidationResponse(c, v.Errors)
		return
	}

	if err := app.models.Units.Update(c.Request.Context(), unit); err != nil {
		app.unitWriteErrorResponse(c, err)
		return
	}

	if err := app.writeJSON(c, http.StatusOK, envelope{"unit": unit}, nil); err != nil {
		app.serverErrorResponse(c, err)
	}
}

// unitWriteErrorResponse maps an error from UnitModel.Insert or Update to
// the right response.
func (app *application) unitWriteErrorResponse(c *gin.Context, err error) {
	switch {
	case errors.Is(err, data.ErrPropertyNotFound):
		app.notFoundResponse(c)
	case errors.Is(err, data.ErrDuplicateUnitCode):
		app.failedValidationResponse(c, map[string]string{"unit_code": "already exists on this property"})
	case errors.Is(err, data.ErrEditConflict):
		app.editConflictResponse(c)
	default:
		app.serverErrorResponse(c, err)
	}
}
