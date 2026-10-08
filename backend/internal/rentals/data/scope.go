package data

import (
	"context"
	"fmt"
	"time"

	"github.com/codercollo/willcoll/backend/internal/rentals/db"
	"github.com/codercollo/willcoll/backend/internal/rentals/db/sqlc"
	"github.com/google/uuid"
)

// ScopeKind names the resource a route's :id path parameter refers to.
type ScopeKind string

const (
	ScopeProperty    ScopeKind = "property"
	ScopeUnit        ScopeKind = "unit"
	ScopeLease       ScopeKind = "lease"
	ScopeLandlord    ScopeKind = "landlord"
	ScopePayment     ScopeKind = "payment"
	ScopeLedgerEntry ScopeKind = "ledger-entry"
)

// ScopeModel answers "does this resource exist for this caller?" for the
// property-scope check in front of every route that names a resource. It runs
// in the caller's tenant transaction, so a context carrying a property scope
// (auth.WithPropertyScope) makes anything outside that scope absent.
type ScopeModel struct {
	Store   db.Store
	Timeout time.Duration
}

// Exists reports whether the resource of kind and id is visible to ctx's
// caller: in their tenant and, when they are property-scoped, in their scope.
func (m ScopeModel) Exists(ctx context.Context, tenantID uuid.UUID, kind ScopeKind, id uuid.UUID) (bool, error) {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	var found bool
	err := m.Store.ExecTenantTx(ctx, tenantID, func(q sqlc.Querier) error {
		var err error
		switch kind {
		case ScopeProperty:
			found, err = q.ScopeHasProperty(ctx, id)
		case ScopeUnit:
			found, err = q.ScopeHasUnit(ctx, id)
		case ScopeLease:
			found, err = q.ScopeHasLease(ctx, id)
		case ScopeLandlord:
			found, err = q.ScopeHasLandlord(ctx, id)
		case ScopePayment:
			found, err = q.ScopeHasPayment(ctx, id)
		case ScopeLedgerEntry:
			found, err = q.ScopeHasLedgerEntry(ctx, id)
		default:
			err = fmt.Errorf("data: unknown scope kind %q", kind)
		}
		return err
	})
	return found, err
}
