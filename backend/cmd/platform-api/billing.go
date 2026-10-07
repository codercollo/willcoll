package main

import (
	"errors"
	"net/http"
	"strings"

	"github.com/codercollo/willcoll/backend/internal/platform/data"
	"github.com/codercollo/willcoll/backend/internal/shared/payhero"
	sharedpdf "github.com/codercollo/willcoll/backend/internal/shared/pdf"
	"github.com/codercollo/willcoll/backend/internal/shared/reconcile"
	"github.com/codercollo/willcoll/backend/internal/shared/validator"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Platform billing (build-phases.txt 3c-iv-b1): a manager pays Willcoll a
// subscription per product through PayHero billing channel. This is the one
// implementation every product API's own billing routes delegate to
// (build-phases.txt 3c-iv-b2), so an invoice started from any of them is
// priced and settled the same way. Routes are scoped by :product so one
// manager can hold independent subscriptions to more than one product.

// validProduct checks the :product path param, recording a 404-shaped
// response itself since an unknown product is a routing error, not a
// validation one.
func (app *application) validProduct(c *gin.Context) (string, bool) {
	product := c.Param("product")
	if !validator.PermittedValue(product, data.ProductRentals, data.ProductEstates, data.ProductWaste) {
		app.notFoundResponse(c)
		return "", false
	}
	return product, true
}

// billingErrorResponse maps BillingModel's sentinel errors to responses.
func (app *application) billingErrorResponse(c *gin.Context, err error) {
	switch {
	case errors.Is(err, data.ErrRecordNotFound):
		app.notFoundResponse(c)
	case errors.Is(err, data.ErrEditConflict):
		app.editConflictResponse(c)
	case errors.Is(err, data.ErrUsageUnavailable):
		app.errorResponse(c, http.StatusServiceUnavailable, "billing data unavailable, contact support")
	case errors.Is(err, data.ErrPlanNotAvailable):
		app.errorResponse(c, http.StatusUnprocessableEntity, "plan not available")
	case errors.Is(err, data.ErrNoPlan), errors.Is(err, data.ErrPlanTooSmall), errors.Is(err, data.ErrInvalidInput):
		app.errorResponse(c, http.StatusUnprocessableEntity, err.Error())
	case errors.Is(err, data.ErrNotDevelopment):
		app.notFoundResponse(c)
	default:
		app.serverErrorResponse(c, err)
	}
}

// listSubscriptionPlansHandler handles GET /v1/billing/:product/plans.
func (app *application) listSubscriptionPlansHandler(c *gin.Context) {
	product, ok := app.validProduct(c)
	if !ok {
		return
	}
	manager := contextGetManager(c)
	if manager.IsAnonymous() {
		app.authenticationRequiredResponse(c)
		return
	}

	plans, err := app.models.Billing.Plans(c.Request.Context(), manager.ID, product, app.config.env == "development")
	if err != nil {
		app.billingErrorResponse(c, err)
		return
	}
	// The firm's reported billable count (module_usage), so the plan picker
	// can disable a plan whose unit cap is below it. 0 when the product has
	// not reported yet.
	unitCount := int32(0)
	if usage, err := app.models.Usage.Get(c.Request.Context(), manager.ID, product); err == nil {
		unitCount = usage.BillableCount
	} else if !errors.Is(err, data.ErrRecordNotFound) {
		app.serverErrorResponse(c, err)
		return
	}
	if err := app.writeJSON(c, http.StatusOK, envelope{"plans": plans, "unit_count": unitCount}, nil); err != nil {
		app.serverErrorResponse(c, err)
	}
}

// showSubscriptionHandler handles GET /v1/billing/:product/subscription.
func (app *application) showSubscriptionHandler(c *gin.Context) {
	product, ok := app.validProduct(c)
	if !ok {
		return
	}
	manager := contextGetManager(c)
	if manager.IsAnonymous() {
		app.authenticationRequiredResponse(c)
		return
	}

	current, err := app.models.Billing.Current(c.Request.Context(), manager.ID, product)
	if err != nil {
		app.billingErrorResponse(c, err)
		return
	}
	if err := app.writeJSON(c, http.StatusOK, envelope{"subscription": current}, nil); err != nil {
		app.serverErrorResponse(c, err)
	}
}

// renewSubscriptionHandler handles POST /v1/billing/:product/subscription/renew.
// Body: {"plan_id": ..., "phone": ...}, plan_id needed only for a manager
// with no subscription to product yet. phone is optional and overrides the
// manager's stored phone for this STK push only; it does not change the
// manager's saved phone. It opens a pending invoice and asks PayHero to
// prompt that phone through the billing channel; the subscription turns
// active when the callback arrives, not before.
func (app *application) renewSubscriptionHandler(c *gin.Context) {
	product, ok := app.validProduct(c)
	if !ok {
		return
	}
	manager := contextGetManager(c)
	if manager.IsAnonymous() {
		app.authenticationRequiredResponse(c)
		return
	}

	var input struct {
		PlanID *uuid.UUID `json:"plan_id"`
		Phone  string     `json:"phone"`
	}
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
	renewal, err := app.models.Billing.StartRenewal(ctx, manager.ID, product, input.PlanID)
	if err != nil {
		app.billingErrorResponse(c, err)
		return
	}
	if renewal.Invoice.Amount.Cents()%100 != 0 {
		app.failInvoice(c, manager.ID, renewal.Invoice.ID)
		v := validator.New()
		v.AddError("plan", "the price must be a whole number of shillings to be paid by M-Pesa")
		app.failedValidationResponse(c, v.Errors)
		return
	}

	phone := renewal.Phone
	if input.Phone != "" {
		phone = input.Phone
	}
	phone = reconcile.NormalizePhone(phone)
	if !strings.HasPrefix(phone, "+254") || len(phone) != 13 {
		app.failInvoice(c, manager.ID, renewal.Invoice.ID)
		v := validator.New()
		v.AddError("phone", "must be a Kenyan mobile number")
		app.failedValidationResponse(c, v.Errors)
		return
	}

	_, err = app.payhero.STKPush(ctx, payhero.STKPushRequest{
		Amount:            renewal.Invoice.Amount.Cents() / 100,
		PhoneNumber:       strings.TrimPrefix(phone, "+"),
		ChannelID:         app.config.payhero.billingChannelID,
		ExternalReference: renewal.Invoice.ID.String(),
		CustomerName:      renewal.FirmName,
		CallbackURL:       app.callbackURL("/v1/webhooks/payhero/subscriptions"),
	})
	if err != nil {
		app.failPush(ctx, err, func(string) error {
			return app.models.Billing.FailInvoice(ctx, manager.ID, renewal.Invoice.ID)
		})
		app.pushErrorResponse(c, err)
		return
	}

	app.audit(c, product, "subscription.renewal_started", "invoice", &renewal.Invoice.ID, renewal.Plan.Name)

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

// listSubscriptionInvoicesHandler handles GET /v1/billing/:product/invoices.
func (app *application) listSubscriptionInvoicesHandler(c *gin.Context) {
	product, ok := app.validProduct(c)
	if !ok {
		return
	}
	manager := contextGetManager(c)
	if manager.IsAnonymous() {
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

	invoices, metadata, err := app.models.Billing.Invoices(c.Request.Context(), manager.ID, product, filters)
	if err != nil {
		app.billingErrorResponse(c, err)
		return
	}
	if err := app.writeJSON(c, http.StatusOK, envelope{"invoices": invoices, "metadata": metadata}, nil); err != nil {
		app.serverErrorResponse(c, err)
	}
}

// showSubscriptionInvoicePDFHandler handles
// GET /v1/billing/:product/invoices/:id/pdf: Willcoll's own invoice to the
// manager, rendered on demand.
func (app *application) showSubscriptionInvoicePDFHandler(c *gin.Context) {
	if _, ok := app.validProduct(c); !ok {
		return
	}
	manager := contextGetManager(c)
	if manager.IsAnonymous() {
		app.authenticationRequiredResponse(c)
		return
	}
	id, err := app.readIDParam(c)
	if err != nil {
		app.notFoundResponse(c)
		return
	}

	doc, err := app.models.Billing.InvoiceForPDF(c.Request.Context(), manager.ID, id)
	if err != nil {
		app.billingErrorResponse(c, err)
		return
	}
	b, err := sharedpdf.BuildSubscriptionInvoice(sharedpdf.SubscriptionInvoice{
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

// writePDF sends a rendered document. Documents are rendered on demand and
// never stored, and hold a tenant's financial data, so they are not cached.
func (app *application) writePDF(c *gin.Context, filename string, pdfBytes []byte) {
	c.Header("Content-Disposition", `inline; filename="`+filename+`"`)
	c.Header("Cache-Control", "private, no-store")
	c.Data(http.StatusOK, "application/pdf", pdfBytes)
}
