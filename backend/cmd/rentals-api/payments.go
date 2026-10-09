package main

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/codercollo/willcoll/backend/internal/rentals/data"
	"github.com/codercollo/willcoll/backend/internal/rentals/db/sqlc"
	"github.com/codercollo/willcoll/backend/internal/shared/validator"
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
	// The dashboard's "cash payments waiting over 48 hours" count rides along.
	summary, err := app.models.Payments.PendingSummary(c.Request.Context(), tenantID)
	if err != nil {
		app.billingErrorResponse(c, err)
		return
	}
	if err := app.writeJSON(c, http.StatusOK, envelope{"payments": payments, "metadata": metadata, "summary": summary}, nil); err != nil {
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
	app.audit(c, "payment.allocated", "payment", &paymentID, "allocated to unit "+input.UnitID.String())
	if err := app.writeJSON(c, http.StatusOK, envelope{"message": "payment allocated"}, nil); err != nil {
		app.serverErrorResponse(c, err)
	}
}

// actorIdentity is the signed-in principal's id and the role label it is
// recorded under: a staff member's own id and role, or the firm login's id and
// "manager". It always comes from the session, never from a request body.
func (app *application) actorIdentity(c *gin.Context) (uuid.UUID, string) {
	if staff := contextGetStaff(c); !staff.IsAnonymous() {
		return staff.ID, staff.Role
	}
	return contextGetManager(c).ID, "manager"
}

// confirmPaymentHandler handles POST /v1/payments/:id/confirm.
//
// A pending hand-entered payment (an officer's "cash received") is posted to the
// ledger now, exactly once, and the confirmer and the time are recorded; the
// person who recorded it cannot confirm it (403), and confirming again is a 409.
// For any other payment it accepts what the engine applied on its own best guess.
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
	confirmerID, confirmerRole := app.actorIdentity(c)
	decision, err := app.models.Payments.ConfirmAs(c.Request.Context(), tenantID, confirmerID, paymentID)
	if err != nil {
		if errors.Is(err, data.ErrSelfConfirm) {
			app.logDenied(c, "payment recorded by the same person")
		}
		app.billingErrorResponse(c, err)
		return
	}
	if decision.Pending {
		recorded := "recorded by " + decision.RecordedByRole
		if decision.RecordedBy != nil {
			recorded += " " + decision.RecordedBy.String()
		}
		app.audit(c, "payment.confirmed", "payment", &paymentID,
			"confirmed by "+confirmerRole+" "+confirmerID.String()+", "+recorded+": KES "+decision.Amount.Display()+" posted to "+decision.Ledger)
	} else {
		app.audit(c, "payment.confirmed", "payment", &paymentID, "confirmed")
	}
	if err := app.writeJSON(c, http.StatusOK, envelope{"message": "payment confirmed"}, nil); err != nil {
		app.serverErrorResponse(c, err)
	}
}

// rejectPaymentHandler handles POST /v1/payments/:id/reject {"reason"}: refuse a
// pending hand-entered payment. Nothing posts. The reason is required. The owner
// is alerted, and the rejection and its alert commit together.
func (app *application) rejectPaymentHandler(c *gin.Context) {
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
	var input struct {
		Reason string `json:"reason"`
	}
	if err := app.readJSON(c, &input); err != nil {
		app.badRequestResponse(c, err)
		return
	}
	v := validator.New()
	v.Check(strings.TrimSpace(input.Reason) != "", "reason", "must be provided")
	v.Check(len(input.Reason) <= data.MaxRejectReasonLen, "reason", "must not be more than 500 bytes long")
	if !v.Valid() {
		app.failedValidationResponse(c, v.Errors)
		return
	}

	rejecterID, rejecterRole := app.actorIdentity(c)
	reason := strings.TrimSpace(input.Reason)
	alert := func(ctx context.Context, q sqlc.Querier, d data.PaymentDecision) error {
		target := "KES " + d.Amount.Display() + " on " + d.Ledger + " (payment " + d.PaymentID.String() + ")"
		if d.RecordedBy != nil {
			target += ", recorded by " + d.RecordedByRole + " " + d.RecordedBy.String()
		}
		target += "; rejected by " + rejecterRole + ", reason: " + reason
		link := "/payments"
		if d.UnitID != nil {
			link = "/units/" + d.UnitID.String()
		}
		return app.alertHookForKey(c, "payment.rejected:"+d.PaymentID.String(), "A pending cash payment was rejected", target, link)(ctx, q)
	}
	decision, err := app.models.Payments.RejectPending(c.Request.Context(), tenantID, rejecterID, paymentID, reason, alert)
	if err != nil {
		app.billingErrorResponse(c, err)
		return
	}
	app.audit(c, "payment.rejected", "payment", &paymentID,
		"rejected by "+rejecterRole+" "+rejecterID.String()+": KES "+decision.Amount.Display()+", reason: "+reason)
	if err := app.writeJSON(c, http.StatusOK, envelope{"message": "payment rejected"}, nil); err != nil {
		app.serverErrorResponse(c, err)
	}
}
