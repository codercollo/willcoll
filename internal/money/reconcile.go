package money

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

// ReconcileDrift is one ledger account whose cached balance does not equal the
// sum of its entries.
type ReconcileDrift struct {
	AccountID uuid.UUID
	Balance   int64 // KES cents
	EntrySum  int64 // KES cents
}

// ReconcileReport is the result of the daily ledger reconciliation check.
type ReconcileReport struct {
	AccountsChecked int
	DriftAccounts   []ReconcileDrift
}

// Reconcile compares every ledger_accounts.balance cache against the sum of its
// ledger_entries and returns any drift. It is a plain callable method; the
// nightly scheduler that invokes it is Phase 11's concern.
func (s *Service) Reconcile(ctx context.Context) (ReconcileReport, error) {
	var report ReconcileReport

	if err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM ledger_accounts`).Scan(&report.AccountsChecked); err != nil {
		return report, fmt.Errorf("count accounts: %w", err)
	}

	rows, err := s.pool.Query(ctx, `
		SELECT a.id,
		       (a.balance * 100)::bigint,
		       (COALESCE(SUM(e.amount), 0) * 100)::bigint
		FROM ledger_accounts a
		LEFT JOIN ledger_entries e ON e.account_id = a.id
		GROUP BY a.id, a.balance
		HAVING (a.balance * 100)::bigint <> (COALESCE(SUM(e.amount), 0) * 100)::bigint
		ORDER BY a.id`)
	if err != nil {
		return report, fmt.Errorf("query drifts: %w", err)
	}
	defer rows.Close()

	report.DriftAccounts = make([]ReconcileDrift, 0)
	for rows.Next() {
		var d ReconcileDrift
		if err := rows.Scan(&d.AccountID, &d.Balance, &d.EntrySum); err != nil {
			return report, fmt.Errorf("scan drift: %w", err)
		}
		report.DriftAccounts = append(report.DriftAccounts, d)
	}
	if err := rows.Err(); err != nil {
		return report, fmt.Errorf("iterate drifts: %w", err)
	}

	return report, nil
}
