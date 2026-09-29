package data

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/codercollo/willcoll/backend/internal/db"
	"github.com/codercollo/willcoll/backend/internal/db/sqlc"
	"github.com/codercollo/willcoll/backend/internal/validator"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

// Unit statuses (system-design.txt 3.2).
const (
	UnitStatusVacant   = "vacant"
	UnitStatusOccupied = "occupied"
)

var (
	// ErrPropertyNotFound is returned when a unit operation names a
	// property that isn't one of the tenant's active (non-archived) ones.
	ErrPropertyNotFound = errors.New("property not found")

	// ErrDuplicateUnitCode is returned when a property already has a unit
	// with the same unit_code.
	ErrDuplicateUnitCode = errors.New("duplicate unit code")
)

// Unit belongs to exactly one Property (system-design.txt 3.2).
type Unit struct {
	ID          uuid.UUID `json:"id"`
	TenantID    uuid.UUID `json:"-"`
	PropertyID  uuid.UUID `json:"property_id"`
	UnitCode    string    `json:"unit_code"`
	MeterNumber *string   `json:"meter_number,omitempty"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"created_at"`
	Version     int32     `json:"-"`

	// CurrentLease summarises the unit's active lease for the Units tab
	// cards (system-design.txt 6.1). Only list results fill it in, and it
	// is nil for a vacant unit.
	CurrentLease *UnitLease `json:"current_lease,omitempty"`
}

// UnitLease is the part of a unit's active lease shown on its card.
type UnitLease struct {
	LeaseID    uuid.UUID `json:"lease_id"`
	TenantName string    `json:"tenant_name"`
	StartDate  time.Time `json:"start_date"`
	// HasQR is true when the lease has a live door-sticker QR code.
	HasQR bool `json:"has_qr"`
}

// UnitListFilter narrows GetAllForProperty.
type UnitListFilter struct {
	// Status is "", vacant or occupied.
	Status string
	// TenantSearch matches the active lease's tenant name or any of its
	// co-payers' names, word by word and by prefix ("jane wanj" finds
	// "Jane Wanjiku").
	TenantSearch string
}

// maxSearchTerms caps how many words of a search box entry are used.
const maxSearchTerms = 8

var searchTermRX = regexp.MustCompile(`[\p{L}\p{N}]+`)

// prefixTSQuery turns free text into a to_tsquery expression that matches
// every word as a prefix: "Jane  Wanj!" -> "jane:* & wanj:*". Only letters
// and digits survive, so user input can never inject tsquery operators.
// Returns "" when there's nothing to search for.
func prefixTSQuery(text string) string {
	terms := searchTermRX.FindAllString(strings.ToLower(text), maxSearchTerms)
	for i, term := range terms {
		terms[i] = term + ":*"
	}
	return strings.Join(terms, " & ")
}

func ValidateUnit(v *validator.Validator, unit *Unit) {
	v.Check(unit.UnitCode != "", "unit_code", "must be provided")
	v.Check(len(unit.UnitCode) <= 50, "unit_code", "must not be more than 50 bytes long")
	v.Check(validator.PermittedValue(unit.Status, UnitStatusVacant, UnitStatusOccupied), "status", "must be vacant or occupied")
}

// UnitModel is the service layer for units.
type UnitModel struct {
	Store db.Store

	// Timeout bounds each method call; zero means DefaultQueryTimeout.
	Timeout time.Duration
}

// Insert creates unit on one of the tenant's active properties. Returns
// ErrPropertyNotFound or ErrDuplicateUnitCode for those cases.
func (m UnitModel) Insert(ctx context.Context, tenantID uuid.UUID, unit *Unit) error {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	return m.Store.ExecTenantTx(ctx, tenantID, func(q sqlc.Querier) error {
		if err := checkPropertyActive(ctx, q, tenantID, unit.PropertyID); err != nil {
			return err
		}

		row, err := q.CreateUnit(ctx, sqlc.CreateUnitParams{
			TenantID:    tenantID,
			PropertyID:  unit.PropertyID,
			UnitCode:    unit.UnitCode,
			MeterNumber: unit.MeterNumber,
			Status:      unit.Status,
		})
		if err != nil {
			return unitWriteError(err)
		}

		*unit = unitFromRow(row)
		return nil
	})
}

// Get returns the tenant's unit with the given id, or ErrRecordNotFound.
func (m UnitModel) Get(ctx context.Context, tenantID, id uuid.UUID) (*Unit, error) {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	var unit Unit

	err := m.Store.ExecTenantTx(ctx, tenantID, func(q sqlc.Querier) error {
		row, err := q.GetUnit(ctx, sqlc.GetUnitParams{TenantID: tenantID, ID: id})
		if err != nil {
			return notFound(err)
		}

		unit = unitFromRow(row)
		return nil
	})
	if err != nil {
		return nil, err
	}

	return &unit, nil
}

// GetAllForProperty returns a page of the units on one of the tenant's
// active properties, each with its current lease, narrowed by filter. It
// returns ErrPropertyNotFound for a property that isn't the tenant's.
func (m UnitModel) GetAllForProperty(ctx context.Context, tenantID, propertyID uuid.UUID, filter UnitListFilter, filters Filters) ([]*Unit, Metadata, error) {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	var (
		units        []*Unit
		totalRecords int
	)

	err := m.Store.ExecTenantTx(ctx, tenantID, func(q sqlc.Querier) error {
		if err := checkPropertyActive(ctx, q, tenantID, propertyID); err != nil {
			return err
		}

		var status *string
		if filter.Status != "" {
			status = &filter.Status
		}

		rows, err := q.ListUnitsForProperty(ctx, sqlc.ListUnitsForPropertyParams{
			TenantID:    tenantID,
			PropertyID:  propertyID,
			Status:      status,
			TenantQuery: prefixTSQuery(filter.TenantSearch),
			Sort:        filters.Sort,
			PageLimit:   int32(filters.limit()),
			PageOffset:  int32(filters.offset()),
		})
		if err != nil {
			return err
		}

		// Rebuilt on every attempt: ExecTenantTx may re-run this closure.
		units = make([]*Unit, 0, len(rows))
		totalRecords = 0
		for _, row := range rows {
			totalRecords = int(row.TotalRecords)
			u := unitFromRow(row.Unit)
			if row.LeaseID != nil && row.LeaseTenantName != nil && row.LeaseStartDate != nil {
				u.CurrentLease = &UnitLease{
					LeaseID:    *row.LeaseID,
					TenantName: *row.LeaseTenantName,
					StartDate:  *row.LeaseStartDate,
					HasQR:      row.HasQr,
				}
			}
			units = append(units, &u)
		}
		return nil
	})
	if err != nil {
		return nil, Metadata{}, err
	}

	return units, calculateMetadata(totalRecords, filters.Page, filters.PageSize), nil
}

// Update saves unit's code and meter number if its Version still matches
// the stored row, and bumps Version on success. Status is not saved: it
// follows the unit's lease (see LeaseModel). Returns ErrEditConflict or
// ErrDuplicateUnitCode.
func (m UnitModel) Update(ctx context.Context, unit *Unit) error {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	return m.Store.ExecTenantTx(ctx, unit.TenantID, func(q sqlc.Querier) error {
		version, err := q.UpdateUnit(ctx, sqlc.UpdateUnitParams{
			TenantID:    unit.TenantID,
			ID:          unit.ID,
			Version:     unit.Version,
			UnitCode:    unit.UnitCode,
			MeterNumber: unit.MeterNumber,
		})
		if err != nil {
			return unitWriteError(editConflict(err))
		}

		unit.Version = version
		return nil
	})
}

// checkPropertyActive confirms propertyID is one of tenantID's
// non-archived properties. The foreign key alone isn't enough, because FK
// checks ignore RLS.
func checkPropertyActive(ctx context.Context, q sqlc.Querier, tenantID, propertyID uuid.UUID) error {
	ok, err := q.PropertyIsActive(ctx, sqlc.PropertyIsActiveParams{TenantID: tenantID, ID: propertyID})
	if err != nil {
		return err
	}
	if !ok {
		return ErrPropertyNotFound
	}
	return nil
}

func unitWriteError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == pgUniqueViolation && pgErr.ConstraintName == "units_property_id_unit_code_key" {
		return ErrDuplicateUnitCode
	}
	return err
}

func unitFromRow(row sqlc.Unit) Unit {
	return Unit{
		ID:          row.ID,
		TenantID:    row.TenantID,
		PropertyID:  row.PropertyID,
		UnitCode:    row.UnitCode,
		MeterNumber: row.MeterNumber,
		Status:      row.Status,
		CreatedAt:   row.CreatedAt,
		Version:     row.Version,
	}
}
