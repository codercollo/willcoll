package main

import (
	"encoding/csv"
	"errors"
	"net/http"

	"github.com/codercollo/willcoll/backend/internal/data"
	"github.com/codercollo/willcoll/backend/internal/pdf"
	"github.com/gin-gonic/gin"
)

// showElectricityDepositsHandler handles GET
// /v1/properties/:id/electricity/deposits: the property tab's spreadsheet
// (one row per occupied unit, totals) as JSON.
func (app *application) showElectricityDepositsHandler(c *gin.Context) {
	tenantID, ok := contextGetTenantID(c)
	if !ok {
		app.authenticationRequiredResponse(c)
		return
	}
	propertyID, err := app.readIDParam(c)
	if err != nil {
		app.notFoundResponse(c)
		return
	}

	deposits, err := app.models.Electricity.Deposits(c.Request.Context(), tenantID, propertyID)
	if err != nil {
		switch {
		case errors.Is(err, data.ErrRecordNotFound):
			app.notFoundResponse(c)
		default:
			app.serverErrorResponse(c, err)
		}
		return
	}
	if err := app.writeJSON(c, http.StatusOK, envelope{"electricity": deposits}, nil); err != nil {
		app.serverErrorResponse(c, err)
	}
}

// showElectricityDepositsCSVHandler handles GET
// /v1/properties/:id/electricity/deposits.csv: the same spreadsheet as a
// download.
func (app *application) showElectricityDepositsCSVHandler(c *gin.Context) {
	tenantID, ok := contextGetTenantID(c)
	if !ok {
		app.authenticationRequiredResponse(c)
		return
	}
	propertyID, err := app.readIDParam(c)
	if err != nil {
		app.notFoundResponse(c)
		return
	}

	deposits, err := app.models.Electricity.Deposits(c.Request.Context(), tenantID, propertyID)
	if err != nil {
		switch {
		case errors.Is(err, data.ErrRecordNotFound):
			app.notFoundResponse(c)
		default:
			app.serverErrorResponse(c, err)
		}
		return
	}

	c.Header("Content-Disposition", `attachment; filename="electricity-deposits.csv"`)
	c.Header("Cache-Control", "private, no-store")
	c.Header("Content-Type", "text/csv")
	w := csv.NewWriter(c.Writer)
	_ = w.Write([]string{"House No.", "Tenant", "Deposit Required", "Paid", "Balance"})
	for _, r := range deposits.Rows {
		_ = w.Write([]string{r.UnitCode, r.TenantName, r.Required.String(), r.Paid.String(), r.Balance.String()})
	}
	_ = w.Write([]string{"TOTALS", "", deposits.TotalRequired.String(), deposits.TotalPaid.String(), deposits.TotalBalance.String()})
	w.Flush()
}

// showElectricityDepositsPDFHandler handles GET
// /v1/properties/:id/electricity/deposits.pdf.
func (app *application) showElectricityDepositsPDFHandler(c *gin.Context) {
	tenantID, ok := contextGetTenantID(c)
	if !ok {
		app.authenticationRequiredResponse(c)
		return
	}
	propertyID, err := app.readIDParam(c)
	if err != nil {
		app.notFoundResponse(c)
		return
	}

	deposits, err := app.models.Electricity.Deposits(c.Request.Context(), tenantID, propertyID)
	if err != nil {
		switch {
		case errors.Is(err, data.ErrRecordNotFound):
			app.notFoundResponse(c)
		default:
			app.serverErrorResponse(c, err)
		}
		return
	}

	rows := make([]pdf.ElectricityDepositRow, 0, len(deposits.Rows))
	for _, r := range deposits.Rows {
		rows = append(rows, pdf.ElectricityDepositRow{
			HouseNo: r.UnitCode, TenantName: r.TenantName, Required: r.Required, Paid: r.Paid, Balance: r.Balance,
		})
	}
	b, err := pdf.BuildElectricityDeposits(deposits.PropertyName, rows, deposits.TotalRequired, deposits.TotalPaid, deposits.TotalBalance)
	if err != nil {
		app.serverErrorResponse(c, err)
		return
	}
	app.writePDF(c, "electricity-deposits.pdf", b)
}
