package data

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/codercollo/willcoll/backend/internal/db"
	"github.com/codercollo/willcoll/backend/internal/db/sqlc"
	"github.com/codercollo/willcoll/backend/internal/validator"
	"github.com/codercollo/willcoll/backend/pkg/moneyfmt"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

var (
	// ErrDuplicateSlug is returned when a property's slug is already in use.
	// Slugs are globally unique because they appear in the public
	// /pay/{property_slug}/{unit_code} URL.
	ErrDuplicateSlug = errors.New("duplicate property slug")

	// ErrLandlordNotFound is returned when landlord_id doesn't name one of
	// the tenant's own landlords.
	ErrLandlordNotFound = errors.New("landlord not found")

	// ErrPropertyHasActiveLeases is returned by Delete while any unit on
	// the property still has an active lease.
	ErrPropertyHasActiveLeases = errors.New("property has active leases")
)

// Property belongs to exactly one Landlord (system-design.txt 3.2).
type Property struct {
	ID             uuid.UUID      `json:"id"`
	TenantID       uuid.UUID      `json:"-"`
	LandlordID     uuid.UUID      `json:"landlord_id"`
	Name           string         `json:"name"`
	Location       string         `json:"location"`
	Slug           string         `json:"slug"`
	GarbageEnabled bool           `json:"garbage_enabled"`
	GarbageFee     moneyfmt.Money `json:"garbage_fee"`
	// ElectricityEnabled/ElectricityDepositAmount: same shape as garbage's
	// toggle + default amount, but a one-off refundable deposit like the
	// water deposit, not a monthly bill.
	ElectricityEnabled       bool           `json:"electricity_enabled"`
	ElectricityDepositAmount moneyfmt.Money `json:"electricity_deposit_amount"`
	WaterRatePerUnit         moneyfmt.Money `json:"water_rate_per_unit"`
	ManagementFeePercent     float64        `json:"management_fee_percent"`
	PayheroChannelID         *string        `json:"payhero_channel_id,omitempty"`
	// UndergroundCapacityUnits / RooftopCapacityUnits feed NOTE:1's
	// storage-capacity lines. Nil means "not configured": the lines are
	// omitted, never printed as zero.
	UndergroundCapacityUnits *moneyfmt.Money `json:"underground_capacity_units,omitempty"`
	RooftopCapacityUnits     *moneyfmt.Money `json:"rooftop_capacity_units,omitempty"`
	// ReconnectionFee backs the water/garbage bill's "NB: reconnection
	// fee" line. Nil omits the structured line (a print_theme override
	// may still supply free text).
	ReconnectionFee *moneyfmt.Money `json:"reconnection_fee,omitempty"`
	PrintTheme      *PrintTheme     `json:"print_theme,omitempty"`
	// Summary is the card-grid overview, set only where a handler asks for it
	// with AttachSummaries.
	Summary   *PropertySummary `json:"summary,omitempty"`
	CreatedAt time.Time        `json:"created_at"`
	Version   int32            `json:"-"`
}

func ValidateProperty(v *validator.Validator, property *Property) {
	v.Check(property.LandlordID != uuid.Nil, "landlord_id", "must be provided")

	v.Check(property.Name != "", "name", "must be provided")
	v.Check(len(property.Name) <= 500, "name", "must not be more than 500 bytes long")

	v.Check(property.Location != "", "location", "must be provided")

	v.Check(property.Slug != "", "slug", "must be provided")
	v.Check(len(property.Slug) <= 200, "slug", "must not be more than 200 bytes long")
	// /v1/pay/intents/:id/status shares its shape with /v1/pay/:slug/:unit.
	// /pay/inactive is the frontend page a dead QR code lands on.
	v.Check(property.Slug != "intents" && property.Slug != "inactive", "slug", "is reserved")

	v.Check(!property.GarbageFee.IsNegative(), "garbage_fee", "must not be negative")
	v.Check(!property.ElectricityDepositAmount.IsNegative(), "electricity_deposit_amount", "must not be negative")
	v.Check(!property.WaterRatePerUnit.IsNegative(), "water_rate_per_unit", "must not be negative")
	v.Check(property.ManagementFeePercent >= 0 && property.ManagementFeePercent <= 100,
		"management_fee_percent", "must be between 0 and 100")
	if property.UndergroundCapacityUnits != nil {
		v.Check(!property.UndergroundCapacityUnits.IsNegative(), "underground_capacity_units", "must not be negative")
	}
	if property.RooftopCapacityUnits != nil {
		v.Check(!property.RooftopCapacityUnits.IsNegative(), "rooftop_capacity_units", "must not be negative")
	}
}

// PropertyModel is the service layer for properties. It owns the business
// rules that need the database (landlord ownership, slug uniqueness) and
// runs every query through db.Store inside the tenant's RLS-scoped
// transaction.
type PropertyModel struct {
	Store db.Store

	// Timeout bounds each method call; zero means DefaultQueryTimeout.
	Timeout time.Duration
}

// Insert creates property for tenantID, filling in its ID, CreatedAt and
// Version. Returns ErrLandlordNotFound or ErrDuplicateSlug for those cases.
func (m PropertyModel) Insert(ctx context.Context, tenantID uuid.UUID, property *Property) error {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	return m.Store.ExecTenantTx(ctx, tenantID, func(q sqlc.Querier) error {
		if err := checkLandlord(ctx, q, tenantID, property.LandlordID); err != nil {
			return err
		}

		row, err := q.CreateProperty(ctx, sqlc.CreatePropertyParams{
			TenantID:                 tenantID,
			LandlordID:               property.LandlordID,
			Name:                     property.Name,
			Location:                 property.Location,
			Slug:                     property.Slug,
			GarbageEnabled:           property.GarbageEnabled,
			GarbageFee:               property.GarbageFee,
			ElectricityEnabled:       property.ElectricityEnabled,
			ElectricityDepositAmount: property.ElectricityDepositAmount,
			WaterRatePerUnit:         property.WaterRatePerUnit,
			ManagementFeePercent:     property.ManagementFeePercent,
			PayheroChannelID:         property.PayheroChannelID,
			UndergroundCapacityUnits: property.UndergroundCapacityUnits,
			RooftopCapacityUnits:     property.RooftopCapacityUnits,
			ReconnectionFee:          property.ReconnectionFee,
		})
		if err != nil {
			return propertyWriteError(err)
		}

		// The receipt counter is created here, not lazily on first
		// payment: every property has one from the moment it exists.
		if err := q.CreateReceiptCounter(ctx, sqlc.CreateReceiptCounterParams{TenantID: tenantID, PropertyID: row.ID}); err != nil {
			return err
		}

		*property = propertyFromRow(row)
		return nil
	})
}

// Get returns the tenant's property with the given id, or ErrRecordNotFound.
func (m PropertyModel) Get(ctx context.Context, tenantID, id uuid.UUID) (*Property, error) {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	var property Property

	err := m.Store.ExecTenantTx(ctx, tenantID, func(q sqlc.Querier) error {
		row, err := q.GetProperty(ctx, sqlc.GetPropertyParams{TenantID: tenantID, ID: id})
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrRecordNotFound
			}
			return err
		}

		property = propertyFromRow(row)
		return nil
	})
	if err != nil {
		return nil, err
	}

	return &property, nil
}

// GetAll returns a page of the tenant's properties, optionally limited to
// one landlord (landlordID != nil). filters must already have passed
// ValidateFilters, so Sort is on the safelist.
func (m PropertyModel) GetAll(ctx context.Context, tenantID uuid.UUID, landlordID *uuid.UUID, filters Filters) ([]*Property, Metadata, error) {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	var (
		properties   []*Property
		totalRecords int
	)

	err := m.Store.ExecTenantTx(ctx, tenantID, func(q sqlc.Querier) error {
		rows, err := q.ListProperties(ctx, sqlc.ListPropertiesParams{
			TenantID:   tenantID,
			LandlordID: landlordID,
			Sort:       filters.Sort,
			PageLimit:  int32(filters.limit()),
			PageOffset: int32(filters.offset()),
		})
		if err != nil {
			return err
		}

		// Rebuilt from scratch on every attempt: ExecTenantTx re-runs this
		// closure after a serialization failure.
		properties = make([]*Property, 0, len(rows))
		totalRecords = 0
		for _, row := range rows {
			totalRecords = int(row.TotalRecords)
			p := propertyFromRow(row.Property)
			properties = append(properties, &p)
		}
		return nil
	})
	if err != nil {
		return nil, Metadata{}, err
	}

	return properties, calculateMetadata(totalRecords, filters.Page, filters.PageSize), nil
}

// Update saves property if its Version still matches the stored row, and
// bumps Version on success. Returns ErrEditConflict if another request
// updated it first, or ErrLandlordNotFound / ErrDuplicateSlug.
func (m PropertyModel) Update(ctx context.Context, property *Property) error {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	return m.Store.ExecTenantTx(ctx, property.TenantID, func(q sqlc.Querier) error {
		if err := checkLandlord(ctx, q, property.TenantID, property.LandlordID); err != nil {
			return err
		}

		version, err := q.UpdateProperty(ctx, sqlc.UpdatePropertyParams{
			TenantID:                 property.TenantID,
			ID:                       property.ID,
			Version:                  property.Version,
			LandlordID:               property.LandlordID,
			Name:                     property.Name,
			Location:                 property.Location,
			Slug:                     property.Slug,
			GarbageEnabled:           property.GarbageEnabled,
			GarbageFee:               property.GarbageFee,
			ElectricityEnabled:       property.ElectricityEnabled,
			ElectricityDepositAmount: property.ElectricityDepositAmount,
			WaterRatePerUnit:         property.WaterRatePerUnit,
			ManagementFeePercent:     property.ManagementFeePercent,
			PayheroChannelID:         property.PayheroChannelID,
			UndergroundCapacityUnits: property.UndergroundCapacityUnits,
			RooftopCapacityUnits:     property.RooftopCapacityUnits,
			ReconnectionFee:          property.ReconnectionFee,
		})
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrEditConflict
			}
			return propertyWriteError(err)
		}

		property.Version = version
		return nil
	})
}

// Delete archives the tenant's property (a soft delete: deleted_at is set,
// nothing is removed, so its units' ledgers stay intact). From then on it
// is invisible to Get, GetAll and Update. Returns ErrRecordNotFound if it
// doesn't exist or is already archived, and ErrPropertyHasActiveLeases
// while anyone still has an active lease on it.
func (m PropertyModel) Delete(ctx context.Context, tenantID, id uuid.UUID) error {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	return m.Store.ExecTenantTx(ctx, tenantID, func(q sqlc.Querier) error {
		occupied, err := q.PropertyHasActiveLeases(ctx, sqlc.PropertyHasActiveLeasesParams{TenantID: tenantID, PropertyID: id})
		if err != nil {
			return err
		}
		if occupied {
			return ErrPropertyHasActiveLeases
		}

		rows, err := q.ArchiveProperty(ctx, sqlc.ArchivePropertyParams{TenantID: tenantID, ID: id})
		if err != nil {
			return err
		}
		if rows == 0 {
			return ErrRecordNotFound
		}
		return nil
	})
}

// checkLandlord confirms landlordID is one of tenantID's landlords. The
// foreign key alone isn't enough, because FK checks ignore RLS.
func checkLandlord(ctx context.Context, q sqlc.Querier, tenantID, landlordID uuid.UUID) error {
	ok, err := q.LandlordBelongsToTenant(ctx, sqlc.LandlordBelongsToTenantParams{TenantID: tenantID, ID: landlordID})
	if err != nil {
		return err
	}
	if !ok {
		return ErrLandlordNotFound
	}
	return nil
}

// propertyWriteError maps constraint violations from an insert or update
// to this package's sentinel errors.
func propertyWriteError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == pgUniqueViolation && pgErr.ConstraintName == "properties_slug_key" {
		return ErrDuplicateSlug
	}
	return err
}

func propertyFromRow(row sqlc.Property) Property {
	return Property{
		PrintTheme:               decodePrintTheme(row.PrintTheme),
		ID:                       row.ID,
		TenantID:                 row.TenantID,
		LandlordID:               row.LandlordID,
		Name:                     row.Name,
		Location:                 row.Location,
		Slug:                     row.Slug,
		GarbageEnabled:           row.GarbageEnabled,
		GarbageFee:               row.GarbageFee,
		ElectricityEnabled:       row.ElectricityEnabled,
		ElectricityDepositAmount: row.ElectricityDepositAmount,
		WaterRatePerUnit:         row.WaterRatePerUnit,
		ManagementFeePercent:     row.ManagementFeePercent,
		PayheroChannelID:         row.PayheroChannelID,
		UndergroundCapacityUnits: row.UndergroundCapacityUnits,
		RooftopCapacityUnits:     row.RooftopCapacityUnits,
		ReconnectionFee:          row.ReconnectionFee,
		CreatedAt:                row.CreatedAt,
		Version:                  row.Version,
	}
}
