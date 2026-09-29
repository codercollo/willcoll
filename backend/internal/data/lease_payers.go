package data

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/codercollo/willcoll/backend/internal/db/sqlc"
	"github.com/codercollo/willcoll/backend/internal/reconciliation"
	"github.com/codercollo/willcoll/backend/internal/validator"
	"github.com/google/uuid"
)

// maxLeasePayers is how many co-payers a lease may carry. Real leases have a
// spouse or a shop partner or two; a long list is a mistake, and every phone
// on it can request a pay-page code.
const maxLeasePayers = 5

var (
	// ErrTooManyPayers is returned when a lease already has the maximum.
	ErrTooManyPayers = errors.New("a lease can have at most 5 co-payers")

	// ErrDuplicatePayer is returned when a co-payer phone is already on the
	// lease, as its primary phone or another co-payer.
	ErrDuplicatePayer = errors.New("that phone number is already on this lease")

	// ErrLeaseNotFound is returned when a payer is added to a lease that is not
	// the tenant's.
	ErrLeaseNotFound = errors.New("lease not found")
)

// LeasePayer is someone besides the tenant who may pay for the unit.
type LeasePayer struct {
	ID    uuid.UUID `json:"id"`
	Name  string    `json:"name"`
	Phone *string   `json:"phone,omitempty"`
}

func leasePayerFromRow(r sqlc.LeasePayer) LeasePayer {
	return LeasePayer{ID: r.ID, Name: r.Name, Phone: r.Phone}
}

func loadPayers(ctx context.Context, q sqlc.Querier, tenantID, leaseID uuid.UUID) ([]LeasePayer, error) {
	rows, err := q.ListLeasePayers(ctx, sqlc.ListLeasePayersParams{TenantID: tenantID, LeaseID: leaseID})
	if err != nil {
		return nil, err
	}
	out := make([]LeasePayer, 0, len(rows))
	for _, r := range rows {
		out = append(out, leasePayerFromRow(r))
	}
	return out, nil
}

// ValidateLeasePayer checks a co-payer: a name, and an optional phone in
// international form.
func ValidateLeasePayer(v *validator.Validator, p *LeasePayer) {
	p.Name = strings.TrimSpace(p.Name)
	v.Check(p.Name != "", "name", "must be provided")
	v.Check(len(p.Name) <= 200, "name", "must not be more than 200 bytes long")
	if p.Phone != nil {
		phone := strings.TrimSpace(*p.Phone)
		if phone == "" {
			p.Phone = nil
		} else {
			p.Phone = &phone
			v.Check(validator.Matches(phone, validator.PhoneRX), "phone", "must be a valid E.164 phone number")
		}
	}
}

// CheckLeasePayers makes sure a set of co-payers, with the lease primary phone,
// has no repeated phone and is not too long.
func CheckLeasePayers(primaryPhone string, payers []LeasePayer) error {
	if len(payers) > maxLeasePayers {
		return ErrTooManyPayers
	}
	seen := map[string]bool{reconciliation.NormalizePhone(primaryPhone): true}
	for _, p := range payers {
		if p.Phone == nil {
			continue
		}
		n := reconciliation.NormalizePhone(*p.Phone)
		if seen[n] {
			return fmt.Errorf("%w: %s", ErrDuplicatePayer, *p.Phone)
		}
		seen[n] = true
	}
	return nil
}

// AddPayer adds a co-payer to an active or past lease of the tenant.
func (m LeaseModel) AddPayer(ctx context.Context, tenantID, leaseID uuid.UUID, payer *LeasePayer) error {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	return m.Store.ExecTenantTx(ctx, tenantID, func(q sqlc.Querier) error {
		lease, err := q.GetLease(ctx, sqlc.GetLeaseParams{TenantID: tenantID, ID: leaseID})
		if err != nil {
			if errors.Is(notFound(err), ErrRecordNotFound) {
				return ErrLeaseNotFound
			}
			return err
		}
		existing, err := loadPayers(ctx, q, tenantID, leaseID)
		if err != nil {
			return err
		}
		if err := CheckLeasePayers(lease.PrimaryPhone, append(existing, *payer)); err != nil {
			return err
		}

		row, err := q.CreateLeasePayer(ctx, sqlc.CreateLeasePayerParams{
			TenantID: tenantID, LeaseID: leaseID, Name: payer.Name, Phone: payer.Phone,
		})
		if err != nil {
			return err
		}
		*payer = leasePayerFromRow(row)
		return nil
	})
}

// RemovePayer removes a co-payer from a lease. Returns ErrRecordNotFound if it
// is not on that lease.
func (m LeaseModel) RemovePayer(ctx context.Context, tenantID, leaseID, payerID uuid.UUID) error {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	return m.Store.ExecTenantTx(ctx, tenantID, func(q sqlc.Querier) error {
		n, err := q.DeleteLeasePayer(ctx, sqlc.DeleteLeasePayerParams{TenantID: tenantID, LeaseID: leaseID, ID: payerID})
		if err != nil {
			return err
		}
		if n == 0 {
			return ErrRecordNotFound
		}
		return nil
	})
}

// ListForUnit returns a unit's lease history, newest first, each with its
// co-payers. Returns ErrRecordNotFound if the unit is not the tenant's.
func (m LeaseModel) ListForUnit(ctx context.Context, tenantID, unitID uuid.UUID) ([]*Lease, error) {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	var out []*Lease
	err := m.Store.ExecTenantTx(ctx, tenantID, func(q sqlc.Querier) error {
		if _, err := q.GetUnit(ctx, sqlc.GetUnitParams{TenantID: tenantID, ID: unitID}); err != nil {
			return notFound(err)
		}
		rows, err := q.ListLeasesForUnit(ctx, sqlc.ListLeasesForUnitParams{TenantID: tenantID, UnitID: unitID})
		if err != nil {
			return err
		}
		out = make([]*Lease, 0, len(rows))
		for _, r := range rows {
			l := leaseFromRow(r)
			if l.Payers, err = loadPayers(ctx, q, tenantID, l.ID); err != nil {
				return err
			}
			out = append(out, &l)
		}
		return nil
	})
	return out, err
}
