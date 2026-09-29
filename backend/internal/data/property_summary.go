package data

import (
	"context"

	"github.com/codercollo/willcoll/backend/internal/db/sqlc"
	"github.com/codercollo/willcoll/backend/pkg/moneyfmt"
	"github.com/google/uuid"
)

// PropertySummary is one card on the Properties grid (system-design.txt 6.1):
// the landlord, occupancy, and how much rent has been collected against what
// was expected for the period.
type PropertySummary struct {
	Period        moneyfmt.Period `json:"period"`
	LandlordName  string          `json:"landlord_name"`
	UnitsOccupied int             `json:"units_occupied"`
	UnitsVacant   int             `json:"units_vacant"`
	RentExpected  moneyfmt.Money  `json:"rent_expected"`
	RentCollected moneyfmt.Money  `json:"rent_collected"`
}

// AttachSummaries fills Summary on each property for the period, in one query
// for the whole firm. Properties the query does not return (archived since)
// are left without one.
func (m PropertyModel) AttachSummaries(ctx context.Context, tenantID uuid.UUID, period moneyfmt.Period, properties []*Property) error {
	if len(properties) == 0 {
		return nil
	}
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	return m.Store.ExecTenantTx(ctx, tenantID, func(q sqlc.Querier) error {
		rows, err := q.ListPropertySummaries(ctx, sqlc.ListPropertySummariesParams{Period: period.FirstDay(), TenantID: tenantID})
		if err != nil {
			return err
		}
		byID := make(map[uuid.UUID]sqlc.ListPropertySummariesRow, len(rows))
		for _, r := range rows {
			byID[r.PropertyID] = r
		}
		for _, p := range properties {
			r, ok := byID[p.ID]
			if !ok {
				continue
			}
			p.Summary = &PropertySummary{
				Period: period, LandlordName: r.LandlordName, UnitsOccupied: int(r.Occupied), UnitsVacant: int(r.Vacant),
				RentExpected: r.Expected, RentCollected: r.Collected,
			}
		}
		return nil
	})
}
