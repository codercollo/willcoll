package main

import (
	"errors"
	"net/http"
	"time"

	"github.com/codercollo/willcoll/backend/internal/data"
	"github.com/codercollo/willcoll/backend/internal/validator"
	"github.com/codercollo/willcoll/backend/pkg/moneyfmt"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// createPropertyHandler handles POST /v1/properties.
func (app *application) createPropertyHandler(c *gin.Context) {
	tenantID, ok := contextGetTenantID(c)
	if !ok {
		app.authenticationRequiredResponse(c)
		return
	}

	var input struct {
		LandlordID           uuid.UUID      `json:"landlord_id" validate:"required"`
		Name                 string         `json:"name" validate:"required,max=500"`
		Location             string         `json:"location" validate:"required"`
		Slug                 string         `json:"slug" validate:"required,max=200,slug"`
		GarbageEnabled       bool           `json:"garbage_enabled"`
		GarbageFee           moneyfmt.Money `json:"garbage_fee"`
		WaterRatePerUnit     moneyfmt.Money `json:"water_rate_per_unit"`
		ManagementFeePercent float64        `json:"management_fee_percent" validate:"min=0,max=100"`
		PayheroChannelID     *string        `json:"payhero_channel_id"`
	}

	if err := app.readJSON(c, &input); err != nil {
		app.badRequestResponse(c, err)
		return
	}

	property := &data.Property{
		LandlordID:           input.LandlordID,
		Name:                 input.Name,
		Location:             input.Location,
		Slug:                 input.Slug,
		GarbageEnabled:       input.GarbageEnabled,
		GarbageFee:           input.GarbageFee,
		WaterRatePerUnit:     input.WaterRatePerUnit,
		ManagementFeePercent: input.ManagementFeePercent,
		PayheroChannelID:     input.PayheroChannelID,
	}

	v := validator.New()
	v.Struct(input)
	if data.ValidateProperty(v, property); !v.Valid() {
		app.failedValidationResponse(c, v.Errors)
		return
	}

	if err := app.models.Properties.Insert(c.Request.Context(), tenantID, property); err != nil {
		app.propertyWriteErrorResponse(c, err)
		return
	}

	if err := app.writeJSON(c, http.StatusCreated, envelope{"property": property}, nil); err != nil {
		app.serverErrorResponse(c, err)
	}
}

// listPropertiesHandler handles GET /v1/properties. Query parameters:
// landlord_id, sort (name|created_at, "-" for descending), page, page_size.
func (app *application) listPropertiesHandler(c *gin.Context) {
	tenantID, ok := contextGetTenantID(c)
	if !ok {
		app.authenticationRequiredResponse(c)
		return
	}

	var input struct {
		LandlordID *uuid.UUID
		Filters    data.Filters
	}

	qs := c.Request.URL.Query()
	v := validator.New()

	input.LandlordID = app.readUUID(qs, "landlord_id", v)
	input.Filters.Page = app.readInt(qs, "page", 1, v)
	input.Filters.PageSize = app.readInt(qs, "page_size", 20, v)
	input.Filters.Sort = app.readString(qs, "sort", "name")
	input.Filters.SortSafelist = []string{"name", "-name", "created_at", "-created_at"}

	if data.ValidateFilters(v, input.Filters); !v.Valid() {
		app.failedValidationResponse(c, v.Errors)
		return
	}

	properties, metadata, err := app.models.Properties.GetAll(c.Request.Context(), tenantID, input.LandlordID, input.Filters)
	if err != nil {
		app.serverErrorResponse(c, err)
		return
	}

	// The card grid shows rent collected against expected for a period: the
	// current month unless ?period=YYYY-MM says otherwise.
	period, ok := app.summaryPeriod(c)
	if !ok {
		return
	}
	if err := app.models.Properties.AttachSummaries(c.Request.Context(), tenantID, period, properties); err != nil {
		app.serverErrorResponse(c, err)
		return
	}

	err = app.writeJSON(c, http.StatusOK, envelope{"properties": properties, "metadata": metadata}, nil)
	if err != nil {
		app.serverErrorResponse(c, err)
	}
}

// showPropertyHandler handles GET /v1/properties/:id.
func (app *application) showPropertyHandler(c *gin.Context) {
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

	period, ok := app.summaryPeriod(c)
	if !ok {
		return
	}
	if err := app.models.Properties.AttachSummaries(c.Request.Context(), tenantID, period, []*data.Property{property}); err != nil {
		app.serverErrorResponse(c, err)
		return
	}

	if err := app.writeJSON(c, http.StatusOK, envelope{"property": property}, nil); err != nil {
		app.serverErrorResponse(c, err)
	}
}

// updatePropertyHandler handles PATCH /v1/properties/:id.
func (app *application) updatePropertyHandler(c *gin.Context) {
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
		LandlordID           *uuid.UUID      `json:"landlord_id"`
		Name                 *string         `json:"name" validate:"omitnil,min=1,max=500"`
		Location             *string         `json:"location" validate:"omitnil,min=1"`
		Slug                 *string         `json:"slug" validate:"omitnil,max=200,slug"`
		WaterRatePerUnit     *moneyfmt.Money `json:"water_rate_per_unit"`
		ManagementFeePercent *float64        `json:"management_fee_percent" validate:"omitnil,min=0,max=100"`
		PayheroChannelID     *string         `json:"payhero_channel_id"`
	}

	if err := app.readJSON(c, &input); err != nil {
		app.badRequestResponse(c, err)
		return
	}

	if input.LandlordID != nil {
		property.LandlordID = *input.LandlordID
	}
	if input.Name != nil {
		property.Name = *input.Name
	}
	if input.Location != nil {
		property.Location = *input.Location
	}
	if input.Slug != nil {
		property.Slug = *input.Slug
	}
	if input.WaterRatePerUnit != nil {
		property.WaterRatePerUnit = *input.WaterRatePerUnit
	}
	if input.ManagementFeePercent != nil {
		property.ManagementFeePercent = *input.ManagementFeePercent
	}
	if input.PayheroChannelID != nil {
		property.PayheroChannelID = input.PayheroChannelID
	}

	v := validator.New()
	v.Struct(input)
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

// deletePropertyHandler handles DELETE /v1/properties/:id. It archives
// rather than deletes (migration 000019): the property disappears from the
// API but its units' ledger history is kept.
func (app *application) deletePropertyHandler(c *gin.Context) {
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

	err = app.models.Properties.Delete(c.Request.Context(), tenantID, id)
	if err != nil {
		switch {
		case errors.Is(err, data.ErrRecordNotFound):
			app.notFoundResponse(c)
		case errors.Is(err, data.ErrPropertyHasActiveLeases):
			app.errorResponse(c, http.StatusConflict, "this property still has active leases; terminate them before deleting it")
		default:
			app.serverErrorResponse(c, err)
		}
		return
	}

	if err := app.writeJSON(c, http.StatusOK, envelope{"message": "property successfully deleted"}, nil); err != nil {
		app.serverErrorResponse(c, err)
	}
}

// propertyWriteErrorResponse maps an error from PropertyModel.Insert or
// Update to the right response: rule violations the database detected are
// reported like any other validation failure (422).
func (app *application) propertyWriteErrorResponse(c *gin.Context, err error) {
	switch {
	case errors.Is(err, data.ErrLandlordNotFound):
		app.failedValidationResponse(c, map[string]string{"landlord_id": "must be one of your landlords"})
	case errors.Is(err, data.ErrDuplicateSlug):
		app.failedValidationResponse(c, map[string]string{"slug": "is already in use"})
	case errors.Is(err, data.ErrEditConflict):
		app.editConflictResponse(c)
	default:
		app.serverErrorResponse(c, err)
	}
}

// updatePropertyPrintThemeHandler handles PUT /v1/properties/:id/print-theme.
// The body is {"print_theme": {...}}; a null print_theme clears it, so
// documents fall back to the property's and landlord's own details.
func (app *application) updatePropertyPrintThemeHandler(c *gin.Context) {
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

	var input struct {
		PrintTheme *data.PrintTheme `json:"print_theme"`
	}
	if err := app.readJSON(c, &input); err != nil {
		app.badRequestResponse(c, err)
		return
	}

	v := validator.New()
	if input.PrintTheme != nil {
		data.ValidatePrintTheme(v, input.PrintTheme)
	}
	if !v.Valid() {
		app.failedValidationResponse(c, v.Errors)
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

	if err := app.models.Properties.SetPrintTheme(c.Request.Context(), property, input.PrintTheme); err != nil {
		switch {
		case errors.Is(err, data.ErrEditConflict):
			app.editConflictResponse(c)
		default:
			app.serverErrorResponse(c, err)
		}
		return
	}

	if err := app.writeJSON(c, http.StatusOK, envelope{"property": property}, nil); err != nil {
		app.serverErrorResponse(c, err)
	}
}

// summaryPeriod reads the optional ?period=YYYY-MM of a property summary,
// defaulting to the current month, and writes a 422 if it is malformed.
func (app *application) summaryPeriod(c *gin.Context) (moneyfmt.Period, bool) {
	raw := c.Request.URL.Query().Get("period")
	if raw == "" {
		return moneyfmt.NewPeriod(time.Now()), true
	}
	v := validator.New()
	p := app.readPeriodQuery(c.Request.URL.Query(), v)
	if !v.Valid() {
		app.failedValidationResponse(c, v.Errors)
		return moneyfmt.Period{}, false
	}
	return p, true
}
