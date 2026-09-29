package data

import (
	"context"
	"time"

	"github.com/codercollo/willcoll/backend/internal/db"
	"github.com/codercollo/willcoll/backend/internal/db/sqlc"
	"github.com/codercollo/willcoll/backend/pkg/moneyfmt"
	"github.com/google/uuid"
)

// ElectricityDepositRow is one occupied unit's electricity deposit line for
// the property tab. Paid is derived (Required - Balance), not stored: the
// deposit's balance is the single source of truth, same as every other
// ledger type.
type ElectricityDepositRow struct {
	UnitID     uuid.UUID      `json:"unit_id"`
	UnitCode   string         `json:"unit_code"`
	TenantName string         `json:"tenant_name"`
	Required   moneyfmt.Money `json:"deposit_required"`
	Paid       moneyfmt.Money `json:"paid"`
	Balance    moneyfmt.Money `json:"balance"`
}

// ElectricityDeposits is the property tab's spreadsheet: one row per
// occupied unit, plus totals.
type ElectricityDeposits struct {
	PropertyName  string                  `json:"property_name"`
	Enabled       bool                    `json:"enabled"`
	Rows          []ElectricityDepositRow `json:"rows"`
	TotalRequired moneyfmt.Money          `json:"total_required"`
	TotalPaid     moneyfmt.Money          `json:"total_paid"`
	TotalBalance  moneyfmt.Money          `json:"total_balance"`
}

// ElectricityModel is the service layer for the electricity deposit
// property tab (the toggle itself lives on PropertyModel, matching
// garbage's split between updatePropertyGarbageHandler and the garbage
// tab's own model).
type ElectricityModel struct {
	Store db.Store

	// Timeout bounds each method call; zero means DefaultQueryTimeout.
	Timeout time.Duration
}

// Deposits returns the property's electricity deposit spreadsheet: every
// occupied unit, whether or not it has a deposit (required = 0 for one that
// doesn't, mirroring the garbage tab's "not billed" rows).
func (m ElectricityModel) Deposits(ctx context.Context, tenantID, propertyID uuid.UUID) (*ElectricityDeposits, error) {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	var out ElectricityDeposits
	err := m.Store.ExecTenantTx(ctx, tenantID, func(q sqlc.Querier) error {
		property, err := q.GetProperty(ctx, sqlc.GetPropertyParams{TenantID: tenantID, ID: propertyID})
		if err != nil {
			return notFound(err)
		}
		rows, err := q.ListElectricityDeposits(ctx, sqlc.ListElectricityDepositsParams{TenantID: tenantID, PropertyID: propertyID})
		if err != nil {
			return err
		}
		out = ElectricityDeposits{PropertyName: property.Name, Enabled: property.ElectricityEnabled, Rows: make([]ElectricityDepositRow, 0, len(rows))}
		for _, r := range rows {
			paid := r.DepositRequired.Sub(r.Balance)
			out.Rows = append(out.Rows, ElectricityDepositRow{
				UnitID: r.UnitID, UnitCode: r.UnitCode, TenantName: r.TenantName,
				Required: r.DepositRequired, Paid: paid, Balance: r.Balance,
			})
			out.TotalRequired = out.TotalRequired.Add(r.DepositRequired)
			out.TotalPaid = out.TotalPaid.Add(paid)
			out.TotalBalance = out.TotalBalance.Add(r.Balance)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &out, nil
}
