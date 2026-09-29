package main

import (
	"net/http"
	"strings"

	"github.com/codercollo/willcoll/backend/internal/data"
	"github.com/codercollo/willcoll/backend/internal/validator"
	"github.com/codercollo/willcoll/backend/pkg/moneyfmt"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// listPaymentsForReviewHandler handles GET /v1/payments/review: the payments a
// manager still has to look at, in one list rather than a chase per message:
// unmatched, matched but not placed, and placed by the engine on a guess.
func (app *application) listPaymentsForReviewHandler(c *gin.Context) {
	tenantID, ok := contextGetTenantID(c)
	if !ok {
		app.authenticationRequiredResponse(c)
		return
	}

	v := validator.New()
	var filters data.Filters
	qs := c.Request.URL.Query()
	filters.Page = app.readInt(qs, "page", 1, v)
	filters.PageSize = app.readInt(qs, "page_size", 20, v)
	filters.Sort = "-received_at"
	filters.SortSafelist = []string{"-received_at"}
	if data.ValidateFilters(v, filters); !v.Valid() {
		app.failedValidationResponse(c, v.Errors)
		return
	}

	payments, metadata, err := app.models.Payments.ListForReview(c.Request.Context(), tenantID, filters)
	if err != nil {
		app.billingErrorResponse(c, err)
		return
	}
	if err := app.writeJSON(c, http.StatusOK, envelope{"payments": payments, "metadata": metadata}, nil); err != nil {
		app.serverErrorResponse(c, err)
	}
}

// allocatePaymentHandler handles POST /v1/payments/:id/allocate: a manager
// places a payment the engine could not. Body: {"unit_id", "allocations":
// [{"ledger_type", "amount"}]}. The allocations must add up to exactly the
// payment; only a payment with nothing allocated yet can be placed.
func (app *application) allocatePaymentHandler(c *gin.Context) {
	manager := contextGetManager(c)
	tenantID, ok := contextGetTenantID(c)
	if manager.IsAnonymous() || !ok {
		app.authenticationRequiredResponse(c)
		return
	}
	paymentID, err := app.readIDParam(c)
	if err != nil {
		app.notFoundResponse(c)
		return
	}

	var input struct {
		UnitID      uuid.UUID `json:"unit_id"`
		Allocations []struct {
			LedgerType string          `json:"ledger_type"`
			Amount     *moneyfmt.Money `json:"amount"`
		} `json:"allocations"`
	}
	if err := app.readJSON(c, &input); err != nil {
		app.badRequestResponse(c, err)
		return
	}

	v := validator.New()
	v.Check(input.UnitID != uuid.Nil, "unit_id", "must be provided")
	v.Check(len(input.Allocations) > 0, "allocations", "must contain at least one allocation")
	v.Check(len(input.Allocations) <= 5, "allocations", "must not contain more than five allocations")
	allocations := make([]data.Allocation, 0, len(input.Allocations))
	seen := map[string]bool{}
	for _, a := range input.Allocations {
		lt := strings.ToUpper(strings.TrimSpace(a.LedgerType))
		v.Check(validator.PermittedValue(lt, ledgerTypeSafelist...), "allocations", "ledger_type must be one of rent, water, garbage, rent_deposit, water_deposit, electricity_deposit")
		v.Check(a.Amount != nil && a.Amount.IsPositive(), "allocations", "every allocation needs an amount greater than zero")
		v.Check(!seen[lt], "allocations", "each ledger type may appear only once")
		seen[lt] = true
		if a.Amount != nil {
			allocations = append(allocations, data.Allocation{LedgerType: lt, Amount: *a.Amount})
		}
	}
	if !v.Valid() {
		app.failedValidationResponse(c, v.Errors)
		return
	}

	if err := app.models.Payments.MatchAndAllocate(c.Request.Context(), tenantID, manager.ID, paymentID, input.UnitID, allocations); err != nil {
		app.billingErrorResponse(c, err)
		return
	}
	if err := app.writeJSON(c, http.StatusOK, envelope{"message": "payment allocated"}, nil); err != nil {
		app.serverErrorResponse(c, err)
	}
}

// confirmPaymentHandler handles POST /v1/payments/:id/confirm: a manager
// accepts a payment the engine applied on its own best guess.
func (app *application) confirmPaymentHandler(c *gin.Context) {
	tenantID, ok := contextGetTenantID(c)
	if !ok {
		app.authenticationRequiredResponse(c)
		return
	}
	paymentID, err := app.readIDParam(c)
	if err != nil {
		app.notFoundResponse(c)
		return
	}
	if err := app.models.Payments.Confirm(c.Request.Context(), tenantID, paymentID); err != nil {
		app.billingErrorResponse(c, err)
		return
	}
	if err := app.writeJSON(c, http.StatusOK, envelope{"message": "payment confirmed"}, nil); err != nil {
		app.serverErrorResponse(c, err)
	}
}
