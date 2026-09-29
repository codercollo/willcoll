package data

import (
	"context"
	"errors"
	"time"

	"github.com/codercollo/willcoll/backend/internal/db"
	"github.com/codercollo/willcoll/backend/internal/db/sqlc"
	"github.com/codercollo/willcoll/backend/internal/validator"
	"github.com/codercollo/willcoll/backend/pkg/moneyfmt"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

// Lease statuses (system-design.txt 3.2).
const (
	LeaseStatusActive     = "active"
	LeaseStatusTerminated = "terminated"
)

var (
	// ErrDuplicateActiveLease is returned when a unit would end up with two
	// active leases (the one_active_lease_per_unit partial unique index).
	ErrDuplicateActiveLease = errors.New("unit already has an active lease")

	// ErrUnitNotFound is returned when a lease names a unit that isn't one
	// of the tenant's.
	ErrUnitNotFound = errors.New("unit not found")
)

// Lease is 0..N historical per Unit, exactly 0 or 1 active
// (system-design.txt 3.2).
type Lease struct {
	ID                 uuid.UUID      `json:"id"`
	TenantID           uuid.UUID      `json:"-"`
	UnitID             uuid.UUID      `json:"unit_id"`
	TenantName         string         `json:"tenant_name"`
	PrimaryPhone       string         `json:"primary_phone"`
	RentAmount         moneyfmt.Money `json:"rent_amount"`
	RentDepositAmount  moneyfmt.Money `json:"rent_deposit_amount"`
	WaterDepositAmount moneyfmt.Money `json:"water_deposit_amount"`
	// ElectricityDepositAmount is set only when the property has
	// electricity enabled; charged the same way as the other deposits at
	// lease start. Immutable after creation, same as the other deposits —
	// see EnableElectricityDeposit for switching it on mid-lease.
	ElectricityDepositAmount moneyfmt.Money `json:"electricity_deposit_amount"`
	// GarbageBilled is the per-lease garbage toggle: landlords often
	// don't bill garbage for every unit, so this decides whether a
	// garbage run charges this unit, not properties.garbage_enabled
	// alone (which only gates the feature).
	GarbageBilled bool       `json:"garbage_billed"`
	StartDate     time.Time  `json:"start_date"`
	EndDate       *time.Time `json:"end_date,omitempty"`
	Status        string     `json:"status"`
	CreatedAt     time.Time  `json:"created_at"`
	Version       int32      `json:"-"`

	// Payers are the co-payers on the lease (system-design.txt 2): the "OR"
	// names on the payments schedule. Always an array in JSON, never null.
	Payers []LeasePayer `json:"payers"`
}

func ValidateLease(v *validator.Validator, lease *Lease) {
	v.Check(lease.TenantName != "", "tenant_name", "must be provided")
	v.Check(lease.PrimaryPhone != "", "primary_phone", "must be provided")
	v.Check(validator.Matches(lease.PrimaryPhone, validator.PhoneRX), "primary_phone", "must be a valid E.164 phone number")
	v.Check(lease.RentAmount.IsPositive(), "rent_amount", "must be greater than zero")
	v.Check(!lease.RentDepositAmount.IsNegative(), "rent_deposit_amount", "must not be negative")
	v.Check(!lease.WaterDepositAmount.IsNegative(), "water_deposit_amount", "must not be negative")
	v.Check(!lease.ElectricityDepositAmount.IsNegative(), "electricity_deposit_amount", "must not be negative")
	v.Check(!lease.StartDate.IsZero(), "start_date", "must be provided")
	if lease.EndDate != nil {
		v.Check(!lease.EndDate.Before(lease.StartDate), "end_date", "must not be before start_date")
	}
}

// LeaseModel is the service layer for leases.
type LeaseModel struct {
	Store db.Store

	// Timeout bounds each method call; zero means DefaultQueryTimeout.
	Timeout time.Duration
}

// Insert creates a lease on one of the tenant's units and, in the same
// transaction, marks the unit occupied and posts the RENT_DEPOSIT /
// WATER_DEPOSIT charge and DEBIT entry for each non-zero deposit
// (system-design.txt 3.2.1, ADR 0002). It all commits or rolls back as one.
// createdBy is the acting manager. Returns ErrUnitNotFound or
// ErrDuplicateActiveLease.
func (m LeaseModel) Insert(ctx context.Context, tenantID, createdBy uuid.UUID, lease *Lease) error {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	return m.Store.ExecTenantTx(ctx, tenantID, func(q sqlc.Querier) error {
		return createLeaseTx(ctx, q, tenantID, createdBy, lease)
	})
}

// createLeaseTx is Insert's body, so a bulk onboarding import can create many
// leases inside one transaction. It runs on the caller's transaction.
func createLeaseTx(ctx context.Context, q sqlc.Querier, tenantID, createdBy uuid.UUID, lease *Lease) error {
	if _, err := q.GetUnit(ctx, sqlc.GetUnitParams{TenantID: tenantID, ID: lease.UnitID}); err != nil {
		if errors.Is(notFound(err), ErrRecordNotFound) {
			return ErrUnitNotFound
		}
		return err
	}

	row, err := q.CreateLease(ctx, sqlc.CreateLeaseParams{
		TenantID:                 tenantID,
		UnitID:                   lease.UnitID,
		TenantName:               lease.TenantName,
		PrimaryPhone:             lease.PrimaryPhone,
		RentAmount:               lease.RentAmount,
		RentDepositAmount:        lease.RentDepositAmount,
		WaterDepositAmount:       lease.WaterDepositAmount,
		ElectricityDepositAmount: lease.ElectricityDepositAmount,
		StartDate:                lease.StartDate,
		EndDate:                  lease.EndDate,
		Status:                   lease.Status,
		GarbageBilled:            lease.GarbageBilled,
	})
	if err != nil {
		return leaseWriteError(err)
	}
	created := leaseFromRow(row)

	for _, p := range lease.Payers {
		payer, err := q.CreateLeasePayer(ctx, sqlc.CreateLeasePayerParams{
			TenantID: tenantID, LeaseID: created.ID, Name: p.Name, Phone: p.Phone,
		})
		if err != nil {
			return err
		}
		created.Payers = append(created.Payers, leasePayerFromRow(payer))
	}

	if err := setUnitStatusForLease(ctx, q, tenantID, created.UnitID, created.Status); err != nil {
		return err
	}

	var deposits []debitCharge
	for _, d := range []struct {
		ledgerType  string
		amount      moneyfmt.Money
		description string
	}{
		{LedgerTypeRentDeposit, created.RentDepositAmount, "Rent deposit — lease start"},
		{LedgerTypeWaterDeposit, created.WaterDepositAmount, "Water deposit — lease start"},
		{LedgerTypeElectricityDeposit, created.ElectricityDepositAmount, "Electricity deposit — lease start"},
	} {
		if !d.amount.IsPositive() {
			continue
		}
		leaseID := created.ID
		deposits = append(deposits, debitCharge{
			UnitID:     created.UnitID,
			LedgerType: d.ledgerType,
			Charge: Charge{
				SourceType:  ChargeSourceLeaseStart,
				SourceID:    &leaseID,
				Period:      created.StartDate,
				Amount:      d.amount,
				Description: d.description,
				CreatedBy:   createdBy,
			},
		})
	}

	if len(deposits) > 0 {
		header := TransactionHeader{
			Type:           TxTypeLeaseStart,
			IdempotencyKey: created.ID.String(), // a lease posts its deposits once
			Description:    "Lease start deposits",
			CreatedBy:      createdBy.String(),
		}
		if err := postCharges(ctx, q, tenantID, header, deposits); err != nil {
			return err
		}
	}

	*lease = created
	return nil
}

// EnableElectricityDeposit retroactively charges the electricity deposit to
// every active lease on the property that has never had one (amount still
// zero), the one time electricity_enabled flips on for a property that
// already has active leases. Idempotent per lease (IdempotencyKey keys on
// the lease id), so a retried call never double-charges. Returns how many
// leases were charged.
func (m LeaseModel) EnableElectricityDeposit(ctx context.Context, tenantID, createdBy, propertyID uuid.UUID, amount moneyfmt.Money) (int, error) {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	if !amount.IsPositive() {
		return 0, nil
	}

	var charged int
	err := m.Store.ExecTenantTx(ctx, tenantID, func(q sqlc.Querier) error {
		leases, err := q.ListActiveLeasesWithoutElectricityDeposit(ctx, sqlc.ListActiveLeasesWithoutElectricityDepositParams{
			TenantID: tenantID, PropertyID: propertyID,
		})
		if err != nil {
			return err
		}
		for _, lease := range leases {
			if err := q.SetLeaseElectricityDeposit(ctx, sqlc.SetLeaseElectricityDepositParams{
				TenantID: tenantID, ID: lease.ID, ElectricityDepositAmount: amount,
			}); err != nil {
				return err
			}
			leaseID := lease.ID
			header := TransactionHeader{
				Type:           TxTypeManualAdjustment,
				IdempotencyKey: "electricity-enable:" + leaseID.String(),
				Description:    "Electricity deposit — enabled for existing lease",
				CreatedBy:      createdBy.String(),
			}
			charge := debitCharge{
				UnitID:     lease.UnitID,
				LedgerType: LedgerTypeElectricityDeposit,
				Charge: Charge{
					SourceType:  ChargeSourceLeaseStart,
					SourceID:    &leaseID,
					Period:      lease.StartDate,
					Amount:      amount,
					Description: "Electricity deposit — enabled for existing lease",
					CreatedBy:   createdBy,
				},
			}
			if err := postCharges(ctx, q, tenantID, header, []debitCharge{charge}); err != nil {
				if isUniqueViolation(err, headerKeyConstraint) {
					continue // already charged by a previous call
				}
				return err
			}
			charged++
		}
		return nil
	})
	return charged, err
}

// Get returns the tenant's lease with the given id, or ErrRecordNotFound.
func (m LeaseModel) Get(ctx context.Context, tenantID, id uuid.UUID) (*Lease, error) {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	var lease Lease

	err := m.Store.ExecTenantTx(ctx, tenantID, func(q sqlc.Querier) error {
		row, err := q.GetLease(ctx, sqlc.GetLeaseParams{TenantID: tenantID, ID: id})
		if err != nil {
			return notFound(err)
		}

		lease = leaseFromRow(row)
		lease.Payers, err = loadPayers(ctx, q, tenantID, lease.ID)
		return err
	})
	if err != nil {
		return nil, err
	}

	return &lease, nil
}

// Update saves an edit or termination if lease's Version still matches the
// stored row, and bumps Version on success. When the status changes, the
// unit's status follows in the same transaction: vacant on termination,
// occupied on reactivation. Deposits and start date aren't updatable
// (they're already on the ledger). Returns ErrEditConflict, or
// ErrDuplicateActiveLease when reactivating a lease on a unit that already
// has another active one.
func (m LeaseModel) Update(ctx context.Context, lease *Lease) error {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	return m.Store.ExecTenantTx(ctx, lease.TenantID, func(q sqlc.Querier) error {
		current, err := q.GetLease(ctx, sqlc.GetLeaseParams{TenantID: lease.TenantID, ID: lease.ID})
		if err != nil {
			return editConflict(err)
		}

		version, err := q.UpdateLease(ctx, sqlc.UpdateLeaseParams{
			TenantID:      lease.TenantID,
			ID:            lease.ID,
			Version:       lease.Version,
			TenantName:    lease.TenantName,
			PrimaryPhone:  lease.PrimaryPhone,
			RentAmount:    lease.RentAmount,
			EndDate:       lease.EndDate,
			Status:        lease.Status,
			GarbageBilled: lease.GarbageBilled,
		})
		if err != nil {
			return leaseWriteError(editConflict(err))
		}

		if lease.Status != current.Status {
			if err := setUnitStatusForLease(ctx, q, lease.TenantID, current.UnitID, lease.Status); err != nil {
				return err
			}
			// A lease that ends takes its unit sticker with it, atomically.
			if lease.Status != LeaseStatusActive {
				if err := revokeQRCodesForLease(ctx, q, lease.TenantID, lease.ID); err != nil {
					return err
				}
			}
		}

		lease.Version = version
		return nil
	})
}

// setUnitStatusForLease keeps units.status in step with the unit's lease:
// an active lease means occupied, a terminated one vacant. It runs inside
// the lease write's transaction, so the two can never disagree, and it's
// the only code path that changes a unit's status.
func setUnitStatusForLease(ctx context.Context, q sqlc.Querier, tenantID, unitID uuid.UUID, leaseStatus string) error {
	status := UnitStatusVacant
	if leaseStatus == LeaseStatusActive {
		status = UnitStatusOccupied
	}
	return q.SetUnitStatus(ctx, sqlc.SetUnitStatusParams{TenantID: tenantID, ID: unitID, Status: status})
}

func leaseWriteError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == pgUniqueViolation && pgErr.ConstraintName == "one_active_lease_per_unit" {
		return ErrDuplicateActiveLease
	}
	return err
}

func leaseFromRow(row sqlc.Lease) Lease {
	return Lease{
		Payers:                   []LeasePayer{},
		ID:                       row.ID,
		TenantID:                 row.TenantID,
		UnitID:                   row.UnitID,
		TenantName:               row.TenantName,
		PrimaryPhone:             row.PrimaryPhone,
		RentAmount:               row.RentAmount,
		RentDepositAmount:        row.RentDepositAmount,
		WaterDepositAmount:       row.WaterDepositAmount,
		ElectricityDepositAmount: row.ElectricityDepositAmount,
		GarbageBilled:            row.GarbageBilled,
		StartDate:                row.StartDate,
		EndDate:                  row.EndDate,
		Status:                   row.Status,
		CreatedAt:                row.CreatedAt,
		Version:                  row.Version,
	}
}
