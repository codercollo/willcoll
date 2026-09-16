package billing

import (
	"context"
	"time"

	"github.com/codercollo/willcoll-sys/internal/money"
	"github.com/codercollo/willcoll-sys/internal/notify"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
)

// Service computes late-fee policy and delegates every ledger write to
// internal/money. It never writes ledger SQL directly.
type Service struct {
	money  *money.Service
	pool   *pgxpool.Pool
	notify *notify.Service
}

// NewService constructs a billing Service. A pool is required so the service
// can read invoices/leases; ledger writes still go only through moneyService.
func NewService(moneyService *money.Service, pool *pgxpool.Pool, notifyService *notify.Service) *Service {
	return &Service{money: moneyService, pool: pool, notify: notifyService}
}

// ChargeLateFeesReport summarizes one ChargeLateFees run.
type ChargeLateFeesReport struct {
	InvoicesCharged int
	AmountCharged   int64 // KES cents
}

// ChargeLateFees finds open rent invoices past their lease's rent_due_day and
// posts one late-fee charge for each. It is callable but not self-scheduling.
func (s *Service) ChargeLateFees(ctx context.Context) (ChargeLateFeesReport, error) {
	var report ChargeLateFeesReport

	rows, err := s.pool.Query(ctx, `
		SELECT i.id, i.organization_id, l.tenant_id, u.property_id,
		       i.period_month, l.rent_due_day, l.late_fee_pct_per_day,
		       l.monthly_rent, l.created_by, t.phone
		FROM invoices i
		JOIN leases l ON l.id = i.lease_id
		JOIN units u ON u.id = l.unit_id
		JOIN tenants t ON t.id = l.tenant_id
		WHERE i.invoice_type = 'rent'
		  AND i.status NOT IN ('paid', 'waived')
		  AND i.amount_paid < i.amount_due`)
	if err != nil {
		return report, err
	}
	defer rows.Close()

	type candidate struct {
		invoiceID      uuid.UUID
		organizationID uuid.UUID
		tenantID       uuid.UUID
		propertyID     uuid.UUID
		periodMonth    time.Time
		rentDueDay     int
		lateFeePct     decimal.Decimal
		monthlyRent    decimal.Decimal
		recordedBy     uuid.UUID
		tenantPhone    string
	}

	for rows.Next() {
		var c candidate
		if err := rows.Scan(
			&c.invoiceID, &c.organizationID, &c.tenantID, &c.propertyID,
			&c.periodMonth, &c.rentDueDay, &c.lateFeePct, &c.monthlyRent, &c.recordedBy,
			&c.tenantPhone,
		); err != nil {
			return report, err
		}

		due := dueDate(c.periodMonth, c.rentDueDay)
		today := time.Now()
		if !today.After(due) {
			continue
		}

		daysLate := int(today.Sub(due).Hours() / 24)
		if daysLate < 1 {
			daysLate = 1
		}

		// fee = monthly_rent * (late_fee_pct / 100) * days_late
		pct := c.lateFeePct.Div(decimal.NewFromInt(100))
		fee := c.monthlyRent.Mul(pct).Mul(decimal.NewFromInt(int64(daysLate))).Round(0)

		if _, err := s.money.ExecuteLateFeeChargeTx(ctx, money.LateFeeTxInput{
			OrganizationID: c.organizationID,
			TenantID:       c.tenantID,
			PropertyID:     c.propertyID,
			InvoiceID:      &c.invoiceID,
			Amount:         fee.IntPart(),
			IdempotencyKey: "latefee:" + c.invoiceID.String(),
			RecordedBy:     c.recordedBy,
		}); err != nil {
			return report, err
		}

		if s.notify != nil {
			if err := s.notify.SendTemplate(ctx, c.organizationID, c.tenantPhone, "tenant_arrears_notice.tmpl", map[string]any{
				"Amount": fee.IntPart(),
			}); err != nil {
				return report, err
			}
		}

		report.InvoicesCharged++
		report.AmountCharged += fee.IntPart()
	}
	if err := rows.Err(); err != nil {
		return report, err
	}

	return report, nil
}

func dueDate(periodMonth time.Time, day int) time.Time {
	first := time.Date(periodMonth.Year(), periodMonth.Month(), 1, 0, 0, 0, 0, time.UTC)
	lastDay := first.AddDate(0, 1, -1).Day()
	if day > lastDay {
		day = lastDay
	}
	return time.Date(periodMonth.Year(), periodMonth.Month(), day, 0, 0, 0, 0, time.UTC)
}
