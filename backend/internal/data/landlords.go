package data

import (
	"context"
	"time"

	"github.com/codercollo/willcoll/backend/internal/db"
	"github.com/codercollo/willcoll/backend/internal/db/sqlc"
	"github.com/codercollo/willcoll/backend/internal/validator"
	"github.com/google/uuid"
)

// Landlord is a data record owned by a Manager (system-design.txt 1.3, 3.2).
type Landlord struct {
	ID                uuid.UUID `json:"id"`
	TenantID          uuid.UUID `json:"-"`
	Name              string    `json:"name"`
	Phone             string    `json:"phone"`
	Email             *string   `json:"email,omitempty"`
	BankName          *string   `json:"bank_name,omitempty"`
	BankAccountName   *string   `json:"bank_account_name,omitempty"`
	BankAccountNumber *string   `json:"bank_account_number,omitempty"`
	CreatedAt         time.Time `json:"created_at"`
	Version           int32     `json:"-"`
}

func ValidateLandlord(v *validator.Validator, landlord *Landlord) {
	v.Check(landlord.Name != "", "name", "must be provided")
	v.Check(len(landlord.Name) <= 500, "name", "must not be more than 500 bytes long")

	v.Check(landlord.Phone != "", "phone", "must be provided")
	v.Check(validator.Matches(landlord.Phone, validator.PhoneRX), "phone", "must be a valid E.164 phone number")
}

// LandlordModel is the service layer for landlords.
type LandlordModel struct {
	Store db.Store

	// Timeout bounds each method call; zero means DefaultQueryTimeout.
	Timeout time.Duration
}

// Insert creates landlord for tenantID, filling in ID, CreatedAt and Version.
func (m LandlordModel) Insert(ctx context.Context, tenantID uuid.UUID, landlord *Landlord) error {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	return m.Store.ExecTenantTx(ctx, tenantID, func(q sqlc.Querier) error {
		row, err := q.CreateLandlord(ctx, sqlc.CreateLandlordParams{
			TenantID:          tenantID,
			Name:              landlord.Name,
			Phone:             landlord.Phone,
			Email:             landlord.Email,
			BankName:          landlord.BankName,
			BankAccountName:   landlord.BankAccountName,
			BankAccountNumber: landlord.BankAccountNumber,
		})
		if err != nil {
			return err
		}

		*landlord = landlordFromRow(row)
		return nil
	})
}

// Get returns the tenant's landlord with the given id, or ErrRecordNotFound.
func (m LandlordModel) Get(ctx context.Context, tenantID, id uuid.UUID) (*Landlord, error) {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	var landlord Landlord

	err := m.Store.ExecTenantTx(ctx, tenantID, func(q sqlc.Querier) error {
		row, err := q.GetLandlord(ctx, sqlc.GetLandlordParams{TenantID: tenantID, ID: id})
		if err != nil {
			return notFound(err)
		}

		landlord = landlordFromRow(row)
		return nil
	})
	if err != nil {
		return nil, err
	}

	return &landlord, nil
}

// GetAll returns a page of the tenant's landlords. filters must already
// have passed ValidateFilters.
func (m LandlordModel) GetAll(ctx context.Context, tenantID uuid.UUID, filters Filters) ([]*Landlord, Metadata, error) {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	var (
		landlords    []*Landlord
		totalRecords int
	)

	err := m.Store.ExecTenantTx(ctx, tenantID, func(q sqlc.Querier) error {
		rows, err := q.ListLandlords(ctx, sqlc.ListLandlordsParams{
			TenantID:   tenantID,
			Sort:       filters.Sort,
			PageLimit:  int32(filters.limit()),
			PageOffset: int32(filters.offset()),
		})
		if err != nil {
			return err
		}

		// Rebuilt on every attempt: ExecTenantTx may re-run this closure.
		landlords = make([]*Landlord, 0, len(rows))
		totalRecords = 0
		for _, row := range rows {
			totalRecords = int(row.TotalRecords)
			l := landlordFromRow(row.Landlord)
			landlords = append(landlords, &l)
		}
		return nil
	})
	if err != nil {
		return nil, Metadata{}, err
	}

	return landlords, calculateMetadata(totalRecords, filters.Page, filters.PageSize), nil
}

// Update saves landlord if its Version still matches the stored row, and
// bumps Version on success. Returns ErrEditConflict otherwise.
func (m LandlordModel) Update(ctx context.Context, landlord *Landlord) error {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	return m.Store.ExecTenantTx(ctx, landlord.TenantID, func(q sqlc.Querier) error {
		version, err := q.UpdateLandlord(ctx, sqlc.UpdateLandlordParams{
			TenantID:          landlord.TenantID,
			ID:                landlord.ID,
			Version:           landlord.Version,
			Name:              landlord.Name,
			Phone:             landlord.Phone,
			Email:             landlord.Email,
			BankName:          landlord.BankName,
			BankAccountName:   landlord.BankAccountName,
			BankAccountNumber: landlord.BankAccountNumber,
		})
		if err != nil {
			return editConflict(err)
		}

		landlord.Version = version
		return nil
	})
}

func landlordFromRow(row sqlc.Landlord) Landlord {
	return Landlord{
		ID:                row.ID,
		TenantID:          row.TenantID,
		Name:              row.Name,
		Phone:             row.Phone,
		Email:             row.Email,
		BankName:          row.BankName,
		BankAccountName:   row.BankAccountName,
		BankAccountNumber: row.BankAccountNumber,
		CreatedAt:         row.CreatedAt,
		Version:           row.Version,
	}
}
