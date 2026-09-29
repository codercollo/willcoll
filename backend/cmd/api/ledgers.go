package main

import (
	"errors"
	"net/http"
	"strings"

	"github.com/codercollo/willcoll/backend/internal/data"
	"github.com/codercollo/willcoll/backend/internal/validator"
	"github.com/gin-gonic/gin"
)

var ledgerTypeSafelist = []string{
	data.LedgerTypeRent, data.LedgerTypeWater, data.LedgerTypeGarbage,
	data.LedgerTypeRentDeposit, data.LedgerTypeWaterDeposit, data.LedgerTypeElectricityDeposit,
}

// showUnitLedgerHandler handles GET /v1/units/:id/ledger/:type — the
// append-only, paginated feed plus current balance for one unit's ledger
// account of the given type.
func (app *application) showUnitLedgerHandler(c *gin.Context) {
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

	// Path uses the lowercase spelling from system-design.txt 4.6
	// (rent|water|garbage|rent_deposit|water_deposit); ledger_accounts.type
	// is stored uppercase.
	ledgerType := strings.ToUpper(c.Param("type"))

	v := validator.New()
	v.Check(validator.PermittedValue(ledgerType, ledgerTypeSafelist...), "type", "must be one of rent, water, garbage, rent_deposit, water_deposit, electricity_deposit")
	if !v.Valid() {
		app.failedValidationResponse(c, v.Errors)
		return
	}

	var filters data.Filters

	qs := c.Request.URL.Query()
	filters.Page = app.readInt(qs, "page", 1, v)
	filters.PageSize = app.readInt(qs, "page_size", 20, v)
	// Newest first by default: a manager reviewing a ledger wants the
	// latest entry at the top.
	filters.Sort = app.readString(qs, "sort", "-created_at")
	filters.SortSafelist = []string{"-created_at", "created_at"}

	if data.ValidateFilters(v, filters); !v.Valid() {
		app.failedValidationResponse(c, v.Errors)
		return
	}

	ledger, err := app.models.Ledger.GetUnitLedger(c.Request.Context(), tenantID, unitID, ledgerType, filters)
	if err != nil {
		switch {
		case errors.Is(err, data.ErrRecordNotFound):
			app.notFoundResponse(c)
		default:
			app.serverErrorResponse(c, err)
		}
		return
	}

	err = app.writeJSON(c, http.StatusOK, envelope{"balance": ledger.Balance, "entries": ledger.Entries, "metadata": ledger.Metadata}, nil)
	if err != nil {
		app.serverErrorResponse(c, err)
	}
}
