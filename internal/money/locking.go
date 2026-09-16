package money

import (
	"context"
	"fmt"
	"sort"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// lockAccounts locks the given ledger accounts one at a time in ascending UUID
// order. Because every caller acquires locks in the same deterministic order,
// concurrent transfers touching the same accounts cannot deadlock on each
// other (simplebank pattern, spec §4.3).
func (s *Service) lockAccounts(ctx context.Context, tx pgx.Tx, accountIDs []uuid.UUID) error {
	ids := append([]uuid.UUID(nil), accountIDs...)
	sort.Slice(ids, func(i, j int) bool {
		return ids[i].String() < ids[j].String()
	})

	for _, id := range ids {
		var locked uuid.UUID
		if err := tx.QueryRow(ctx, `
			SELECT id
			FROM ledger_accounts
			WHERE id = $1
			FOR NO KEY UPDATE`,
			id,
		).Scan(&locked); err != nil {
			return fmt.Errorf("lock account %s: %w", id, err)
		}
	}

	return nil
}
