package main

import (
	"errors"
	"net/http"

	"github.com/codercollo/willcoll/backend/internal/data"
	"github.com/codercollo/willcoll/backend/internal/validator"
	"github.com/codercollo/willcoll/backend/pkg/moneyfmt"
	"github.com/gin-gonic/gin"
)

// Subscription plans are the Super Admin's to define: managers can only read
// them (GET /v1/billing/plans), and until a plan exists no manager can
// subscribe. Plans are never deleted, because subscriptions reference them.

// adminListPlansHandler handles GET /v1/admin/plans, with subscriber counts.
func (app *application) adminListPlansHandler(c *gin.Context) {
	plans, err := app.models.Platform.ListPlans(c.Request.Context())
	if err != nil {
		app.serverErrorResponse(c, err)
		return
	}
	if err := app.writeJSON(c, http.StatusOK, envelope{"plans": plans}, nil); err != nil {
		app.serverErrorResponse(c, err)
	}
}

type planBody struct {
	Name            *string         `json:"name"`
	Price           *moneyfmt.Money `json:"price"`
	BillingInterval *string         `json:"billing_interval"`
	UnitCap         *int32          `json:"unit_cap"`
	IsTest          *bool           `json:"is_test"`
	PricingType     *string         `json:"pricing_type"`
	PerUnitPrice    *moneyfmt.Money `json:"per_unit_price"`
	MinPrice        *moneyfmt.Money `json:"min_price"`
	SortOrder       *int32          `json:"sort_order"`
}

// adminCreatePlanHandler handles POST /v1/admin/plans. Body: {"name",
// "price" (flat) or "per_unit_price"/"min_price" (per_unit), "billing_interval":
// weekly|monthly|quarterly|yearly, "unit_cap", "pricing_type": flat|per_unit,
// "is_test", "sort_order"}. Money is in whole shillings, since subscriptions
// are paid by STK push. is_test: true is only allowed when ENV is
// development — the backend enforces this regardless of what the UI sends.
func (app *application) adminCreatePlanHandler(c *gin.Context) {
	var body planBody
	if err := app.readJSON(c, &body); err != nil {
		app.badRequestResponse(c, err)
		return
	}

	v := validator.New()
	v.Check(body.Name != nil, "name", "must be provided")
	v.Check(body.BillingInterval != nil, "billing_interval", "must be provided")
	if !v.Valid() {
		app.failedValidationResponse(c, v.Errors)
		return
	}
	in := data.PlanInput{Name: *body.Name, BillingInterval: *body.BillingInterval, UnitCap: body.UnitCap, SortOrder: valueOr(body.SortOrder, 0)}
	if body.Price != nil {
		in.Price = *body.Price
	}
	if body.IsTest != nil {
		in.IsTest = *body.IsTest
	}
	if body.PricingType != nil {
		in.PricingType = *body.PricingType
	}
	in.PerUnitPrice, in.MinPrice = body.PerUnitPrice, body.MinPrice
	if data.ValidatePlan(v, &in); !v.Valid() {
		app.failedValidationResponse(c, v.Errors)
		return
	}

	plan, err := app.models.Platform.CreatePlan(c.Request.Context(), in, app.config.env == "development")
	if err != nil {
		if errors.Is(err, data.ErrPlanNotAvailable) {
			app.errorResponse(c, http.StatusForbidden, "test plans can only be created in development")
			return
		}
		app.serverErrorResponse(c, err)
		return
	}
	if err := app.writeJSON(c, http.StatusCreated, envelope{"plan": plan}, nil); err != nil {
		app.serverErrorResponse(c, err)
	}
}

func valueOr(p *int32, fallback int32) int32 {
	if p == nil {
		return fallback
	}
	return *p
}

// adminUpdatePlanHandler handles PATCH /v1/admin/plans/:id. Omitted fields
// are left alone. A new price applies to invoices raised from then on.
func (app *application) adminUpdatePlanHandler(c *gin.Context) {
	id, err := app.readIDParam(c)
	if err != nil {
		app.notFoundResponse(c)
		return
	}

	var body planBody
	if err := app.readJSON(c, &body); err != nil {
		app.badRequestResponse(c, err)
		return
	}

	ctx := c.Request.Context()
	plan, err := app.models.Platform.GetPlan(ctx, id)
	if err != nil {
		switch {
		case errors.Is(err, data.ErrRecordNotFound):
			app.notFoundResponse(c)
		default:
			app.serverErrorResponse(c, err)
		}
		return
	}

	in := data.PlanInput{
		Name: plan.Name, Price: plan.Price, BillingInterval: plan.BillingInterval, UnitCap: plan.UnitCap,
		IsTest: plan.IsTest, PricingType: plan.PricingType, PerUnitPrice: plan.PerUnitPrice, MinPrice: plan.MinPrice, SortOrder: plan.SortOrder,
	}
	if body.Name != nil {
		in.Name = *body.Name
	}
	if body.Price != nil {
		in.Price = *body.Price
	}
	if body.BillingInterval != nil {
		in.BillingInterval = *body.BillingInterval
	}
	if body.UnitCap != nil {
		in.UnitCap = body.UnitCap
	}
	if body.IsTest != nil {
		in.IsTest = *body.IsTest
	}
	if body.PricingType != nil {
		in.PricingType = *body.PricingType
	}
	if body.PerUnitPrice != nil {
		in.PerUnitPrice = body.PerUnitPrice
	}
	if body.MinPrice != nil {
		in.MinPrice = body.MinPrice
	}
	if body.SortOrder != nil {
		in.SortOrder = *body.SortOrder
	}

	v := validator.New()
	if data.ValidatePlan(v, &in); !v.Valid() {
		app.failedValidationResponse(c, v.Errors)
		return
	}

	updated, err := app.models.Platform.UpdatePlan(ctx, id, in, app.config.env == "development")
	if err != nil {
		switch {
		case errors.Is(err, data.ErrRecordNotFound):
			app.notFoundResponse(c)
		case errors.Is(err, data.ErrPlanNotAvailable):
			app.errorResponse(c, http.StatusForbidden, "test plans can only be edited in development")
		default:
			app.serverErrorResponse(c, err)
		}
		return
	}
	if err := app.writeJSON(c, http.StatusOK, envelope{"plan": updated}, nil); err != nil {
		app.serverErrorResponse(c, err)
	}
}

// adminArchivePlanHandler handles POST /v1/admin/plans/:id/archive: hides
// the plan from managers and blocks renewals onto it, leaving existing
// subscribers on their current period and history untouched.
func (app *application) adminArchivePlanHandler(c *gin.Context) {
	id, err := app.readIDParam(c)
	if err != nil {
		app.notFoundResponse(c)
		return
	}
	plan, err := app.models.Platform.ArchivePlan(c.Request.Context(), id)
	if err != nil {
		if errors.Is(err, data.ErrRecordNotFound) {
			app.notFoundResponse(c)
			return
		}
		app.serverErrorResponse(c, err)
		return
	}
	if err := app.writeJSON(c, http.StatusOK, envelope{"plan": plan}, nil); err != nil {
		app.serverErrorResponse(c, err)
	}
}

// adminRestorePlanHandler handles POST /v1/admin/plans/:id/restore.
func (app *application) adminRestorePlanHandler(c *gin.Context) {
	id, err := app.readIDParam(c)
	if err != nil {
		app.notFoundResponse(c)
		return
	}
	plan, err := app.models.Platform.RestorePlan(c.Request.Context(), id)
	if err != nil {
		if errors.Is(err, data.ErrRecordNotFound) {
			app.notFoundResponse(c)
			return
		}
		app.serverErrorResponse(c, err)
		return
	}
	if err := app.writeJSON(c, http.StatusOK, envelope{"plan": plan}, nil); err != nil {
		app.serverErrorResponse(c, err)
	}
}
