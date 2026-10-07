package main

import (
	"errors"
	"net/http"

	"github.com/codercollo/willcoll/backend/internal/platform/data"
	"github.com/gin-gonic/gin"
)

// The Modules page and the product switcher (build-phases.txt 3l,
// system-design.txt E.1): which products a firm holds, and starting a
// product's free trial. Added with the platform frontend; no new query,
// it composes ModuleModel.Get / StartTrial.

var allProducts = []string{data.ProductRentals, data.ProductEstates, data.ProductWaste}

// listModulesHandler handles GET /v1/modules: the signed-in firm's module
// rows, one per product it has ever held. A staff member sees only the
// products in their module_access, so the switcher never offers a product
// they would be refused in.
func (app *application) listModulesHandler(c *gin.Context) {
	tenantID, ok := contextGetTenantID(c)
	if !ok {
		app.authenticationRequiredResponse(c)
		return
	}
	staff := contextGetStaff(c)

	modules := []*data.ManagerModule{}
	for _, product := range allProducts {
		if !staff.IsAnonymous() && !containsString(staff.ModuleAccess, product) {
			continue
		}
		m, err := app.models.Modules.Get(c.Request.Context(), tenantID, product)
		switch {
		case err == nil:
			modules = append(modules, m)
		case errors.Is(err, data.ErrRecordNotFound):
		default:
			app.serverErrorResponse(c, err)
			return
		}
	}

	if err := app.writeJSON(c, http.StatusOK, envelope{"modules": modules}, nil); err != nil {
		app.serverErrorResponse(c, err)
	}
}

// startModuleTrialHandler handles POST /v1/modules/:product/trial (owner
// only): starts the product's free trial once. A repeat call returns the
// existing module row and never extends the trial (StartTrial).
func (app *application) startModuleTrialHandler(c *gin.Context) {
	tenantID, ok := contextGetTenantID(c)
	if !ok {
		app.authenticationRequiredResponse(c)
		return
	}
	product, ok := app.validProduct(c)
	if !ok {
		return
	}

	m, err := app.models.Modules.StartTrial(c.Request.Context(), tenantID, product, app.config.trialDays)
	if err != nil {
		app.serverErrorResponse(c, err)
		return
	}
	app.audit(c, product, "module.trial_started", "module", nil, "started the "+product+" trial")

	if err := app.writeJSON(c, http.StatusCreated, envelope{"module": m}, nil); err != nil {
		app.serverErrorResponse(c, err)
	}
}

func containsString(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
