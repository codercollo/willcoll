package data

import (
	"errors"
	"time"

	"github.com/codercollo/willcoll/backend/pkg/moneyfmt"
)

// UsageStaleAfter is how old a module_usage row can be before it's
// flagged on the Super Admin health page. It is still priced from — a
// renewal must not block on a merely-late background job
// (system-design.txt A.7).
const UsageStaleAfter = 48 * time.Hour

// ErrUsageUnavailable is returned when a per_unit plan has no
// module_usage row to price from. A renewal must refuse rather than
// guess: never price at the plan minimum, never at zero — both would
// under-price a real subscriber, silently.
var ErrUsageUnavailable = errors.New("billing data unavailable")

// PricingResult is what a manager owes for one billing period, plus
// whether the usage behind it is stale (Super Admin health page surfaces
// this; the price is used regardless).
type PricingResult struct {
	Amount moneyfmt.Money
	Stale  bool
}

// PriceFlatOrUsage prices plan for a manager. usage is that manager's
// module_usage row for plan.Product, or nil if they have never reported
// one (UsageModel.Get returns ErrRecordNotFound in that case — the
// caller passes nil through, not a zero-value ModuleUsage, so this
// function can tell "no data" apart from "reported zero").
//
// Pure function, no I/O: the fallback rules below (system-design.txt A.7)
// are unit-tested directly, without a database.
//   - A flat plan never needs usage: priced from Plan.Price.
//   - A per_unit plan with usage == nil: ErrUsageUnavailable. The caller
//     must refuse the renewal (503), not substitute a default price.
//   - A per_unit plan with usage older than UsageStaleAfter: priced
//     anyway, from the stale count, with Stale = true.
func PriceFlatOrUsage(plan Plan, usage *ModuleUsage, now time.Time) (PricingResult, error) {
	if plan.PricingType != PricingPerUnit {
		return PricingResult{Amount: plan.Price}, nil
	}
	if usage == nil {
		return PricingResult{}, ErrUsageUnavailable
	}
	stale := now.Sub(usage.ReportedAt) > UsageStaleAfter
	return PricingResult{Amount: plan.PriceForUnits(usage.BillableCount), Stale: stale}, nil
}
