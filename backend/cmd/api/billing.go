package main

import (
	"errors"
	"net/http"
	"strings"

	"github.com/codercollo/willcoll/backend/internal/data"
	"github.com/codercollo/willcoll/backend/internal/payhero"
	"github.com/codercollo/willcoll/backend/internal/pdf"
	"github.com/codercollo/willcoll/backend/internal/reconciliation"
	"github.com/codercollo/willcoll/backend/internal/validator"
	"github.com/codercollo/willcoll/backend/pkg/moneyfmt"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Platform billing: a manager pays Willcoll a subscription through PayHero
// billing channel (system-design.txt 3.6, 7b). Every manager is a tenant, so
// these all act on the signed-in manager subscription only.

// planOut is a plan as a manager sees it, with computed_price: what this
// manager would actually pay (equal to price for a flat plan; for a
// per_unit plan, max(per_unit_price*units, min_price) at their current unit
// count) — the backend computes it so the frontend never re-implements the
// pricing rule.
type planOut struct {
	data.Plan
	ComputedPrice moneyfmt.Money `json:"computed_price"`
}

// listSubscriptionPlansHandler handles GET /v1/billing/plans.
func (app *application) listSubscriptionPlansHandler(c *gin.Context) {
	tenantID, ok := contextGetTenantID(c)
	if !ok {
		app.authenticationRequiredResponse(c)
		return
	}
	plans, units, err := app.models.Billing.Plans(c.Request.Context(), tenantID)
	if err != nil {
		app.billingErrorResponse(c, err)
		return
	}
	out := make([]planOut, 0, len(plans))
	for _, p := range plans {
		out = append(out, planOut{Plan: p, ComputedPrice: p.PriceForUnits(units)})
	}
	if err := app.writeJSON(c, http.StatusOK, envelope{"plans": out, "unit_count": units}, nil); err != nil {
		app.serverErrorResponse(c, err)
	}
}

// showSubscriptionHandler handles GET /v1/billing/subscription.
func (app *application) showSubscriptionHandler(c *gin.Context) {
	tenantID, ok := contextGetTenantID(c)
	if !ok {
		app.authenticationRequiredResponse(c)
		return
	}
	current, err := app.models.Billing.Current(c.Request.Context(), tenantID)
	if err != nil {
		app.billingErrorResponse(c, err)
		return
	}
	if err := app.writeJSON(c, http.StatusOK, envelope{"subscription": current}, nil); err != nil {
		app.serverErrorResponse(c, err)
	}
}

// renewSubscriptionHandler handles POST /v1/billing/subscription/renew. Body:
// {"plan_id": ..., "phone": ...}, plan_id needed only for a manager with no
// subscription yet. phone is optional and overrides the manager's stored
// phone for this STK push only (e.g. paying from a different M-Pesa line);
// it does not change the manager's saved phone. It opens a pending invoice
// and asks PayHero to prompt that phone through the billing channel; the
// subscription turns active when the callback arrives, not before.
func (app *application) renewSubscriptionHandler(c *gin.Context) {
	tenantID, ok := contextGetTenantID(c)
	if !ok {
		app.authenticationRequiredResponse(c)
		return
	}

	var input struct {
		PlanID *uuid.UUID `json:"plan_id"`
		Phone  string     `json:"phone"`
	}
	// The body is optional: an empty one renews the current plan with the
	// manager's stored phone.
	if c.Request.ContentLength != 0 {
		if err := app.readJSON(c, &input); err != nil {
			app.badRequestResponse(c, err)
			return
		}
	}

	if app.config.payhero.billingChannelID == "" {
		app.errorResponse(c, http.StatusServiceUnavailable, "subscription payments are not available right now")
		return
	}

	ctx := c.Request.Context()
	renewal, err := app.models.Billing.StartRenewal(ctx, tenantID, input.PlanID)
	if err != nil {
		if errors.Is(err, data.ErrPlanNotAvailable) {
			app.errorResponse(c, http.StatusUnprocessableEntity, "plan not available")
			return
		}
		app.billingErrorResponse(c, err)
		return
	}
	if renewal.Plan.Price.Cents()%100 != 0 {
		app.failInvoice(c, tenantID, renewal.Invoice.ID)
		v := validator.New()
		v.AddError("plan", "the price must be a whole number of shillings to be paid by M-Pesa")
		app.failedValidationResponse(c, v.Errors)
		return
	}

	phone := renewal.Phone
	if input.Phone != "" {
		phone = input.Phone
	}
	phone = reconciliation.NormalizePhone(phone)
	if !strings.HasPrefix(phone, "+254") || len(phone) != 13 {
		app.failInvoice(c, tenantID, renewal.Invoice.ID)
		v := validator.New()
		v.AddError("phone", "must be a Kenyan mobile number")
		app.failedValidationResponse(c, v.Errors)
		return
	}

	resp, err := app.payhero.STKPush(ctx, payhero.STKPushRequest{
		Amount:            renewal.Plan.Price.Cents() / 100,
		PhoneNumber:       strings.TrimPrefix(phone, "+"),
		ChannelID:         app.config.payhero.billingChannelID,
		ExternalReference: renewal.Invoice.ID.String(),
		CustomerName:      renewal.FirmName,
		CallbackURL:       app.callbackURL("/v1/webhooks/payhero/subscriptions"),
	})
	if err != nil {
		app.failPush(ctx, err, func(string) error {
			return app.models.Billing.FailInvoice(ctx, tenantID, renewal.Invoice.ID)
		})
		app.pushErrorResponse(c, err)
		return
	}
	_ = resp

	out := envelope{"invoice": renewal.Invoice, "message": "check your phone and enter your M-Pesa PIN"}
	if err := app.writeJSON(c, http.StatusAccepted, out, nil); err != nil {
		app.serverErrorResponse(c, err)
	}
}

func (app *application) failInvoice(c *gin.Context, tenantID, invoiceID uuid.UUID) {
	if err := app.models.Billing.FailInvoice(c.Request.Context(), tenantID, invoiceID); err != nil {
		app.logger.Error("could not mark the invoice failed", "error", err.Error())
	}
}

// listSubscriptionInvoicesHandler handles GET /v1/billing/invoices.
func (app *application) listSubscriptionInvoicesHandler(c *gin.Context) {
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
	filters.Sort = "-created_at"
	filters.SortSafelist = []string{"-created_at"}
	if data.ValidateFilters(v, filters); !v.Valid() {
		app.failedValidationResponse(c, v.Errors)
		return
	}

	invoices, metadata, err := app.models.Billing.Invoices(c.Request.Context(), tenantID, filters)
	if err != nil {
		app.billingErrorResponse(c, err)
		return
	}
	if err := app.writeJSON(c, http.StatusOK, envelope{"invoices": invoices, "metadata": metadata}, nil); err != nil {
		app.serverErrorResponse(c, err)
	}
}

// showSubscriptionInvoicePDFHandler handles GET /v1/billing/invoices/:id/pdf:
// Willcoll own invoice to the manager, rendered on demand.
func (app *application) showSubscriptionInvoicePDFHandler(c *gin.Context) {
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

	doc, err := app.models.Billing.InvoiceForPDF(c.Request.Context(), tenantID, id)
	if err != nil {
		app.billingErrorResponse(c, err)
		return
	}
	b, err := pdf.BuildSubscriptionInvoice(pdf.SubscriptionInvoice{
		InvoiceNo:    "INV-" + strings.ToUpper(doc.Invoice.ID.String()[:8]),
		ManagerName:  doc.ManagerName,
		ManagerEmail: doc.ManagerMail,
		Period:       doc.Invoice.Period,
		IssuedAt:     doc.Invoice.CreatedAt,
		Description:  "Willcoll platform subscription: " + doc.PlanName,
		Amount:       doc.Invoice.Amount,
	})
	if err != nil {
		app.serverErrorResponse(c, err)
		return
	}
	app.writePDF(c, "invoice-"+strings.ToLower(doc.Invoice.ID.String()[:8])+".pdf", b)
}
