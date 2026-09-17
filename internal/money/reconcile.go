package money

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

// ReconcileDrift is one ledger account whose cached balance does not equal the
// sum of its entries.
type ReconcileDrift struct {
	AccountID      uuid.UUID
	OrganizationID uuid.UUID
	Balance        int64 // KES cents
	EntrySum       int64 // KES cents
}

// ReconcileReport is the result of the daily ledger reconciliation check.
type ReconcileReport struct {
	AccountsChecked int
	DriftAccounts   []ReconcileDrift
}

// Notifier is the seam Reconcile uses to alert a super-manager on drift. It's
// satisfied by *notify.Service without internal/money importing
// internal/notify — money.Service stays constructed as Service{pool} alone
// (spec §4.3), and the caller (the nightly scheduler) supplies a notifier.
type Notifier interface {
	SendTemplate(ctx context.Context, orgID uuid.UUID, msisdn, templateFile string, vars map[string]any) error
}

// Reconcile compares every ledger_accounts.balance cache against the sum of
// its ledger_entries; any drift found is SMS-alerted to that Organization's
// super-manager (notifier may be nil to skip alerting, e.g. in tests). It is
// a plain callable method — the nightly scheduler that invokes it is a
// separate concern.
func (s *Service) Reconcile(ctx context.Context, notifier Notifier) (ReconcileReport, error) {
	var report ReconcileReport

	if err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM ledger_accounts`).Scan(&report.AccountsChecked); err != nil {
		return report, fmt.Errorf("count accounts: %w", err)
	}

	rows, err := s.pool.Query(ctx, `
		SELECT a.id,
		       a.organization_id,
		       (a.balance * 100)::bigint,
		       (COALESCE(SUM(e.amount), 0) * 100)::bigint
		FROM ledger_accounts a
		LEFT JOIN ledger_entries e ON e.account_id = a.id
		GROUP BY a.id, a.organization_id, a.balance
		HAVING (a.balance * 100)::bigint <> (COALESCE(SUM(e.amount), 0) * 100)::bigint
		ORDER BY a.id`)
	if err != nil {
		return report, fmt.Errorf("query drifts: %w", err)
	}
	defer rows.Close()

	report.DriftAccounts = make([]ReconcileDrift, 0)
	for rows.Next() {
		var d ReconcileDrift
		if err := rows.Scan(&d.AccountID, &d.OrganizationID, &d.Balance, &d.EntrySum); err != nil {
			return report, fmt.Errorf("scan drift: %w", err)
		}
		report.DriftAccounts = append(report.DriftAccounts, d)
	}
	if err := rows.Err(); err != nil {
		return report, fmt.Errorf("iterate drifts: %w", err)
	}

	if notifier != nil && len(report.DriftAccounts) > 0 {
		if err := s.alertSuperManagers(ctx, notifier, report.DriftAccounts); err != nil {
			return report, fmt.Errorf("alert super-managers: %w", err)
		}
	}

	return report, nil
}

// alertSuperManagers sends one ledger_drift_alert.tmpl SMS per affected
// Organization to that Organization's super-manager.
func (s *Service) alertSuperManagers(ctx context.Context, notifier Notifier, drifts []ReconcileDrift) error {
	driftCountByOrg := make(map[uuid.UUID]int)
	for _, d := range drifts {
		driftCountByOrg[d.OrganizationID]++
	}

	for orgID, count := range driftCountByOrg {
		var (
			phone     string
			brandName string
		)
		err := s.pool.QueryRow(ctx, `
			SELECT u.phone, o.brand_name
			FROM users u
			JOIN organizations o ON o.id = u.organization_id
			WHERE u.organization_id = $1 AND u.role = 'manager' AND u.is_super_manager = true
			LIMIT 1`,
			orgID,
		).Scan(&phone, &brandName)
		if err != nil {
			return fmt.Errorf("lookup super-manager for org %s: %w", orgID, err)
		}

		if err := notifier.SendTemplate(ctx, orgID, phone, "ledger_drift_alert.tmpl", map[string]any{
			"BrandName":  brandName,
			"DriftCount": count,
		}); err != nil {
			return fmt.Errorf("send drift alert for org %s: %w", orgID, err)
		}
	}

	return nil
}
