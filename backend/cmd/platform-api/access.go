package main

import (
	"context"
	"errors"
	"time"

	"github.com/codercollo/willcoll/backend/internal/platform/data"
	"github.com/codercollo/willcoll/backend/pkg/moneyfmt"
	"github.com/google/uuid"
)

// accessFor is a firm's standing in one product: the module's own trial and
// the per-product subscription through data.ComputeAccess, the same function
// every product API's paywall gate uses. A missing module or subscription row
// just means no trial / no subscription yet.
func (app *application) accessFor(ctx context.Context, managerID uuid.UUID, product string) (data.Access, error) {
	var trialEndsAt *time.Time
	module, err := app.models.Modules.Get(ctx, managerID, product)
	switch {
	case err == nil:
		trialEndsAt = module.TrialEndsAt
	case !errors.Is(err, data.ErrRecordNotFound):
		return data.Access{}, err
	}

	var sub *data.PlatformSubscription
	current, err := app.models.Modules.GetSubscription(ctx, managerID, product)
	switch {
	case err == nil:
		sub = current
	case !errors.Is(err, data.ErrRecordNotFound):
		return data.Access{}, err
	}
	return data.ComputeAccess(moneyfmt.Now(), trialEndsAt, sub), nil
}
