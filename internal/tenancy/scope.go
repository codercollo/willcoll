package tenancy

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Scope sets the current organization id on the transaction. Every RLS policy
// reads this setting, so it is the one statement that turns "scoped to this
// Organization" on for the rest of the transaction (§1a). It is called only
// from the tenant-resolution middleware and the one place that creates a new
// Organization's first user inside a transaction.
func (s *Service) Scope(ctx context.Context, tx pgx.Tx, organizationID uuid.UUID) error {
	// set_config(..., is_local => true) is the parameterizable equivalent of
	// `SET LOCAL app.current_org_id = '<uuid>'` — PostgreSQL's extended query
	// protocol does not accept a $1 placeholder in a SET statement's value.
	_, err := tx.Exec(ctx, "SELECT set_config('app.current_org_id', $1, true)", organizationID.String())
	return err
}
