package main

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// showOnboardingHandler handles GET /v1/onboarding: the firm's setup checklist,
// derived from what it has actually done. The dashboard shows it until the
// required steps are done.
func (app *application) showOnboardingHandler(c *gin.Context) {
	tenantID, ok := contextGetTenantID(c)
	if !ok {
		app.authenticationRequiredResponse(c)
		return
	}
	status, err := app.models.Onboarding.Status(c.Request.Context(), tenantID)
	if err != nil {
		app.serverErrorResponse(c, err)
		return
	}
	if err := app.writeJSON(c, http.StatusOK, envelope{"onboarding": status}, nil); err != nil {
		app.serverErrorResponse(c, err)
	}
}
