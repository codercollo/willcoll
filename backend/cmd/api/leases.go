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

// createLeaseHandler handles POST /v1/units/:id/leases. Creating a lease
// also posts any non-zero deposit's charge + ledger entry in the same
// transaction (system-design.txt 3.2.1).
func (app *application) createLeaseHandler(c *gin.Context) {
	tenantID, ok := contextGetTenantID(c)
	if !ok {
		app.authenticationRequiredResponse(c)
		return
	}

	unitID, err := app.readIDParam(c)
	if err != nil {
		app.notFoundResponse(c)
		return
	}

	var input struct {
		TenantName         string         `json:"tenant_name" validate:"required"`
		PrimaryPhone       string         `json:"primary_phone" validate:"required,phone"`
		RentAmount         moneyfmt.Money `json:"rent_amount"`
		RentDepositAmount  moneyfmt.Money `json:"rent_deposit_amount"`
		WaterDepositAmount moneyfmt.Money `json:"water_deposit_amount"`
		StartDate          string         `json:"start_date" validate:"required"`
		Payers             []struct {
			Name  string  `json:"name"`
			Phone *string `json:"phone"`
		} `json:"payers"`
	}

	if err := app.readJSON(c, &input); err != nil {
		app.badRequestResponse(c, err)
		return
	}

	v := validator.New()
	v.Struct(input)

	startDate, dateErr := time.Parse(time.DateOnly, input.StartDate)
	v.Check(dateErr == nil, "start_date", "must be a valid date (YYYY-MM-DD)")

	lease := &data.Lease{
		UnitID:             unitID,
		TenantName:         input.TenantName,
		PrimaryPhone:       input.PrimaryPhone,
		RentAmount:         input.RentAmount,
		RentDepositAmount:  input.RentDepositAmount,
		WaterDepositAmount: input.WaterDepositAmount,
		StartDate:          startDate,
		Status:             data.LeaseStatusActive,
		Payers:             []data.LeasePayer{},
	}
	for _, p := range input.Payers {
		payer := data.LeasePayer{Name: p.Name, Phone: p.Phone}
		data.ValidateLeasePayer(v, &payer)
		lease.Payers = append(lease.Payers, payer)
	}

	if data.ValidateLease(v, lease); !v.Valid() {
		app.failedValidationResponse(c, v.Errors)
		return
	}

	if err := data.CheckLeasePayers(lease.PrimaryPhone, lease.Payers); err != nil {
		app.leasePayerErrorResponse(c, err)
		return
	}

	// The acting manager is the tenant: one login per firm (system-design.txt 1.2).
	err = app.models.Leases.Insert(c.Request.Context(), tenantID, tenantID, lease)
	if err != nil {
		switch {
		case errors.Is(err, data.ErrUnitNotFound):
			app.notFoundResponse(c)
		case errors.Is(err, data.ErrDuplicateActiveLease):
			app.errorResponse(c, http.StatusConflict, "this unit already has an active lease")
		default:
			app.serverErrorResponse(c, err)
		}
		return
	}

	if err := app.writeJSON(c, http.StatusCreated, envelope{"lease": lease}, nil); err != nil {
		app.serverErrorResponse(c, err)
	}
}

// showLeaseHandler handles GET /v1/leases/:id.
func (app *application) showLeaseHandler(c *gin.Context) {
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

	lease, err := app.models.Leases.Get(c.Request.Context(), tenantID, id)
	if err != nil {
		switch {
		case errors.Is(err, data.ErrRecordNotFound):
			app.notFoundResponse(c)
		default:
			app.serverErrorResponse(c, err)
		}
		return
	}

	if err := app.writeJSON(c, http.StatusOK, envelope{"lease": lease}, nil); err != nil {
		app.serverErrorResponse(c, err)
	}
}

// updateLeaseHandler handles PATCH /v1/leases/:id — plain field edits, or
// termination when status is set to 'terminated'.
func (app *application) updateLeaseHandler(c *gin.Context) {
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

	lease, err := app.models.Leases.Get(c.Request.Context(), tenantID, id)
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
		TenantName   *string         `json:"tenant_name" validate:"omitnil,min=1"`
		PrimaryPhone *string         `json:"primary_phone" validate:"omitnil,phone"`
		RentAmount   *moneyfmt.Money `json:"rent_amount"`
		EndDate      *string         `json:"end_date"`
		Status       *string         `json:"status" validate:"omitnil,oneof=active terminated"`
	}

	if err := app.readJSON(c, &input); err != nil {
		app.badRequestResponse(c, err)
		return
	}

	v := validator.New()
	v.Struct(input)

	if input.TenantName != nil {
		lease.TenantName = *input.TenantName
	}
	if input.PrimaryPhone != nil {
		lease.PrimaryPhone = *input.PrimaryPhone
	}
	if input.RentAmount != nil {
		lease.RentAmount = *input.RentAmount
	}
	if input.EndDate != nil {
		endDate, err := time.Parse(time.DateOnly, *input.EndDate)
		v.Check(err == nil, "end_date", "must be a valid date (YYYY-MM-DD)")
		lease.EndDate = &endDate
	}
	if input.Status != nil {
		lease.Status = *input.Status
	}

	if data.ValidateLease(v, lease); !v.Valid() {
		app.failedValidationResponse(c, v.Errors)
		return
	}

	if err := app.models.Leases.Update(c.Request.Context(), lease); err != nil {
		switch {
		case errors.Is(err, data.ErrEditConflict):
			app.editConflictResponse(c)
		case errors.Is(err, data.ErrDuplicateActiveLease):
			app.errorResponse(c, http.StatusConflict, "this unit already has another active lease")
		default:
			app.serverErrorResponse(c, err)
		}
		return
	}

	if err := app.writeJSON(c, http.StatusOK, envelope{"lease": lease}, nil); err != nil {
		app.serverErrorResponse(c, err)
	}
}

// leasePayerErrorResponse maps the co-payer rules to answers.
func (app *application) leasePayerErrorResponse(c *gin.Context, err error) {
	switch {
	case errors.Is(err, data.ErrLeaseNotFound), errors.Is(err, data.ErrRecordNotFound):
		app.notFoundResponse(c)
	case errors.Is(err, data.ErrTooManyPayers), errors.Is(err, data.ErrDuplicatePayer):
		app.errorResponse(c, http.StatusUnprocessableEntity, err.Error())
	default:
		app.billingErrorResponse(c, err)
	}
}

// addLeasePayerHandler handles POST /v1/leases/:id/payers. Body: {"name",
// "phone"}: someone besides the tenant who may pay for the unit. Their phone
// (E.164, optional) can request the pay-page code and identifies paybill
// payments; their name prints as an "OR" name on the schedule.
func (app *application) addLeasePayerHandler(c *gin.Context) {
	tenantID, ok := contextGetTenantID(c)
	if !ok {
		app.authenticationRequiredResponse(c)
		return
	}
	leaseID, err := app.readIDParam(c)
	if err != nil {
		app.notFoundResponse(c)
		return
	}

	var input struct {
		Name  string  `json:"name"`
		Phone *string `json:"phone"`
	}
	if err := app.readJSON(c, &input); err != nil {
		app.badRequestResponse(c, err)
		return
	}
	payer := &data.LeasePayer{Name: input.Name, Phone: input.Phone}
	v := validator.New()
	data.ValidateLeasePayer(v, payer)
	if !v.Valid() {
		app.failedValidationResponse(c, v.Errors)
		return
	}

	if err := app.models.Leases.AddPayer(c.Request.Context(), tenantID, leaseID, payer); err != nil {
		app.leasePayerErrorResponse(c, err)
		return
	}
	if err := app.writeJSON(c, http.StatusCreated, envelope{"payer": payer}, nil); err != nil {
		app.serverErrorResponse(c, err)
	}
}

// removeLeasePayerHandler handles DELETE /v1/leases/:id/payers/:payerId.
func (app *application) removeLeasePayerHandler(c *gin.Context) {
	tenantID, ok := contextGetTenantID(c)
	if !ok {
		app.authenticationRequiredResponse(c)
		return
	}
	leaseID, err := app.readIDParam(c)
	payerID, perr := uuid.Parse(c.Param("payerId"))
	if err != nil || perr != nil {
		app.notFoundResponse(c)
		return
	}

	if err := app.models.Leases.RemovePayer(c.Request.Context(), tenantID, leaseID, payerID); err != nil {
		app.leasePayerErrorResponse(c, err)
		return
	}
	if err := app.writeJSON(c, http.StatusOK, envelope{"message": "co-payer removed"}, nil); err != nil {
		app.serverErrorResponse(c, err)
	}
}

// listUnitLeasesHandler handles GET /v1/units/:id/leases: the unit's lease
// history, newest first, each with its co-payers.
func (app *application) listUnitLeasesHandler(c *gin.Context) {
	tenantID, ok := contextGetTenantID(c)
	if !ok {
		app.authenticationRequiredResponse(c)
		return
	}
	unitID, err := app.readIDParam(c)
	if err != nil {
		app.notFoundResponse(c)
		return
	}
	leases, err := app.models.Leases.ListForUnit(c.Request.Context(), tenantID, unitID)
	if err != nil {
		app.leasePayerErrorResponse(c, err)
		return
	}
	if err := app.writeJSON(c, http.StatusOK, envelope{"leases": leases}, nil); err != nil {
		app.serverErrorResponse(c, err)
	}
}
