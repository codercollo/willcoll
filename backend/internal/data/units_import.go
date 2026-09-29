package data

import (
	"context"
	"errors"
	"fmt"

	"github.com/codercollo/willcoll/backend/internal/db/sqlc"
	"github.com/google/uuid"
)

// MaxImportUnits is the most units one bulk import may add.
const MaxImportUnits = 500

// ErrImportRejected is returned by ImportUnits when some rows cannot be
// imported. Nothing was written; ImportError lists every problem.
var ErrImportRejected = errors.New("the import has errors; nothing was imported")

// ImportRowError is one problem in an import. Row is the row number in the
// file, counting the header as row 1.
type ImportRowError struct {
	Row     int    `json:"row"`
	Field   string `json:"field"`
	Message string `json:"message"`
}

// ImportError carries every row problem, wrapping ErrImportRejected.
type ImportError struct{ Rows []ImportRowError }

func (e *ImportError) Error() string {
	return fmt.Sprintf("%v (%d problems)", ErrImportRejected, len(e.Rows))
}
func (e *ImportError) Unwrap() error { return ErrImportRejected }

// ImportUnit is one parsed row of an import, with its file row number.
type ImportUnit struct {
	Row         int
	UnitCode    string
	MeterNumber *string
}

// ImportUnits adds units to a property in one transaction: either every row
// is added or none is. Before writing it checks each row against the units the
// property already has, so a clash is reported by row rather than as a database
// error. With dryRun it does the checks and writes nothing. Returns
// ErrPropertyNotFound, or an *ImportError listing every clash.
func (m UnitModel) ImportUnits(ctx context.Context, tenantID, propertyID uuid.UUID, rows []ImportUnit, dryRun bool) ([]Unit, error) {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	var created []Unit
	err := m.Store.ExecTenantTx(ctx, tenantID, func(q sqlc.Querier) error {
		created = nil
		if err := checkPropertyActive(ctx, q, tenantID, propertyID); err != nil {
			return err
		}

		existing, err := q.ListUnitCodes(ctx, sqlc.ListUnitCodesParams{TenantID: tenantID, PropertyID: propertyID})
		if err != nil {
			return err
		}
		taken := make(map[string]bool, len(existing))
		for _, c := range existing {
			taken[c] = true
		}
		var problems []ImportRowError
		for _, r := range rows {
			if taken[r.UnitCode] {
				problems = append(problems, ImportRowError{Row: r.Row, Field: "unit_code", Message: "already exists on this property"})
			}
		}
		if len(problems) > 0 {
			return &ImportError{Rows: problems}
		}
		if dryRun {
			return nil
		}

		for _, r := range rows {
			row, err := q.CreateUnit(ctx, sqlc.CreateUnitParams{
				TenantID: tenantID, PropertyID: propertyID, UnitCode: r.UnitCode, MeterNumber: r.MeterNumber, Status: UnitStatusVacant,
			})
			if err != nil {
				return unitWriteError(err)
			}
			created = append(created, unitFromRow(row))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return created, nil
}
