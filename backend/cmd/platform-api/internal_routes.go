package main

import (
	"errors"
	"net/http"

	"github.com/codercollo/willcoll/backend/internal/platform/data"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Service-to-service surface (docs/security/c3-spec.md C3.3). /internal/v1/*
// is for product APIs only:
//
//   - It accepts the per-service credential (SERVICE_TOKEN, see
//     internal/shared/auth/service.go) and nothing else. A user session,
//     however privileged, is never consulted: requireService looks only at
//     the Authorization header.
//   - It is reachable only on the internal network. Caddy does not route it
//     and both Caddyfiles answer 404 for /internal/*, so the public listener
//     is never the way in.
//   - The credential is never logged (it rides a header, not the URL) and is
//     never sent to a browser.

// requireService refuses (401) any request without a valid service
// credential, before any handler or session-derived state is looked at.
func (app *application) requireService() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !app.config.service.Enabled() || !app.config.service.Verify(c.Request) {
			// One answer for "not configured", "missing" and "wrong": nothing
			// to learn about which. The attempt is logged without the value.
			app.logger.Warn("service request refused", "path", c.FullPath(), "ip", c.ClientIP())
			c.Header("WWW-Authenticate", "Bearer")
			app.errorResponse(c, http.StatusUnauthorized, "invalid or missing service credential")
			c.Abort()
			return
		}
		c.Next()
	}
}

// serviceHealthHandler handles GET /internal/v1/health: a product API's
// reachability and credential check against platform.
func (app *application) serviceHealthHandler(c *gin.Context) {
	if err := app.writeJSON(c, http.StatusOK, envelope{"status": "available"}, nil); err != nil {
		app.serverErrorResponse(c, err)
	}
}

// serviceReportUsageHandler handles POST /internal/v1/usage: a product API
// reports one firm's billable count for its own product
// (system-design.txt A.7), the same upsert a direct module_usage write makes.
func (app *application) serviceReportUsageHandler(c *gin.Context) {
	var input struct {
		ManagerID     string `json:"manager_id"`
		Product       string `json:"product"`
		BillableCount *int32 `json:"billable_count"`
	}
	if err := app.readJSON(c, &input); err != nil {
		app.badRequestResponse(c, err)
		return
	}

	errs := map[string]string{}
	managerID, err := uuid.Parse(input.ManagerID)
	if err != nil {
		errs["manager_id"] = "must be a valid id"
	}
	switch input.Product {
	case data.ProductRentals, data.ProductEstates, data.ProductWaste:
	default:
		errs["product"] = "must be a known product"
	}
	if input.BillableCount == nil || *input.BillableCount < 0 {
		errs["billable_count"] = "must be zero or more"
	}
	if len(errs) > 0 {
		app.failedValidationResponse(c, errs)
		return
	}

	usage, err := app.models.Usage.Upsert(c.Request.Context(), managerID, input.Product, *input.BillableCount)
	if err != nil {
		if errors.Is(err, data.ErrRecordNotFound) {
			app.notFoundResponse(c)
			return
		}
		app.serverErrorResponse(c, err)
		return
	}
	if err := app.writeJSON(c, http.StatusOK, envelope{"usage": usage}, nil); err != nil {
		app.serverErrorResponse(c, err)
	}
}
