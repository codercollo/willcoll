package main

import (
	"errors"
	"net/http"
	"strings"

	"github.com/codercollo/willcoll/backend/internal/rentals/data"
	"github.com/codercollo/willcoll/backend/internal/shared/validator"
	"github.com/gin-gonic/gin"
)

// payoutField is one landlord payout-destination field: where the landlord's
// money is sent. Changing any of them is owner-only, needs step-up, and alerts
// the owner (docs/security/authorization.md; the 24h cooling-off is a Phase 5
// remittances item). A payout field added later (M-Pesa number, paybill/till,
// branch, account reference) is listed here and nowhere else.
type payoutField struct {
	name string
	sent *string // what the request sent; nil when absent
	now  *string // what the landlord has now
	// secret fields are shown masked in the alert; the rest are not shown at all.
	secret bool
}

func derefOr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// maskPayoutValue shows only the last four characters, "none" for empty.
func maskPayoutValue(s string) string {
	switch {
	case s == "":
		return "none"
	case len(s) <= 4:
		return "****"
	}
	return "****" + s[len(s)-4:]
}

// changedPayoutFields returns the fields the request sends with a value
// different from the current one (an echo is not a change), and the alert text
// describing them, e.g. "Acme Ltd: bank account number ****1234 to ****9876".
func changedPayoutFields(name string, fields []payoutField) (changed bool, target string) {
	var parts []string
	for _, f := range fields {
		if f.sent == nil || *f.sent == derefOr(f.now) {
			continue
		}
		changed = true
		if f.secret {
			parts = append(parts, f.name+" "+maskPayoutValue(derefOr(f.now))+" to "+maskPayoutValue(*f.sent))
		} else {
			parts = append(parts, f.name+" changed")
		}
	}
	return changed, name + ": " + strings.Join(parts, ", ")
}

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

	// Payout destination set at creation: owner-only, step-up, owner alert.
	// On a new landlord every non-empty payout field is a change from nothing.
	payoutChanged, payoutTarget := changedPayoutFields(landlord.Name, []payoutField{
		{name: "bank name", sent: input.BankName},
		{name: "bank account name", sent: input.BankAccountName},
		{name: "bank account number", sent: input.BankAccountNumber, secret: true},
	})
	if !app.requireOwnerForPayoutChange(c, payoutChanged) {
		return
	}
	var hooks []data.TxHook
	if payoutChanged {
		hooks = append(hooks, app.ownerAlertHook(c, "landlord.payout_created", "new", "A landlord payout destination was changed", payoutTarget, "/landlords"))
	}

	if err := app.models.Landlords.Insert(c.Request.Context(), tenantID, landlord, hooks...); err != nil {
		app.serverErrorResponse(c, err)
		return
	}
	app.audit(c, "landlord.created", "landlord", &landlord.ID, landlord.Name)

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

	// Payout destination: re-sending the current value is not a change; a
	// changed one is owner-only with step-up, refused before anything is applied.
	payoutChanged, payoutTarget := changedPayoutFields(landlord.Name, []payoutField{
		{name: "bank name", sent: input.BankName, now: landlord.BankName},
		{name: "bank account name", sent: input.BankAccountName, now: landlord.BankAccountName},
		{name: "bank account number", sent: input.BankAccountNumber, now: landlord.BankAccountNumber, secret: true},
	})
	if !app.requireOwnerForPayoutChange(c, payoutChanged) {
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

	var hooks []data.TxHook
	if payoutChanged {
		hooks = append(hooks, app.ownerAlertHook(c, "landlord.payout_updated", landlord.ID.String(), "A landlord payout destination was changed", payoutTarget, "/landlords"))
	}
	if err := app.models.Landlords.Update(c.Request.Context(), landlord, hooks...); err != nil {
		switch {
		case errors.Is(err, data.ErrEditConflict):
			app.editConflictResponse(c)
		default:
			app.serverErrorResponse(c, err)
		}
		return
	}
	app.audit(c, "landlord.updated", "landlord", &landlord.ID, landlord.Name)

	if err := app.writeJSON(c, http.StatusOK, envelope{"landlord": landlord}, nil); err != nil {
		app.serverErrorResponse(c, err)
	}
}
