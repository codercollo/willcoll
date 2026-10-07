package data

import (
	"time"

	"github.com/codercollo/willcoll/backend/pkg/moneyfmt"
)

// DefaultTrialDays is how long a module's free trial lasts unless
// TRIAL_DAYS says otherwise (build-phases.txt 3f-3: per (manager,
// product), via ModuleModel.StartTrial's trial_ends_at on manager_modules
// — not this manager-wide constant's old use on managers.trial_ends_at,
// which activation no longer writes).
const DefaultTrialDays = 7

// TrialEnd is when a trial that starts at start ends, days later.
func TrialEnd(start time.Time, days int) time.Time {
	if days <= 0 {
		days = DefaultTrialDays
	}
	return start.Add(time.Duration(days) * 24 * time.Hour)
}

// What a firm may do in a product, derived from that product's free trial
// and its subscription (build-phases.txt 3i-b: moved here from
// internal/rentals/data, which no longer has any billing of its own — every
// product API computes access from willcoll_platform with this one
// function).
//
//	subscribed  an active (or trialing) subscription whose paid period has not ended
//	trial       no live subscription, but the free trial is still running
//	expired     neither: the firm must buy a plan; the product is closed except
//	            for what's needed to pay. Tenants can still pay their rent.
const (
	AccessSubscribed = "subscribed"
	AccessTrial      = "trial"
	AccessExpired    = "expired"
)

// Access is a firm's standing in one product.
type Access struct {
	State string `json:"state"`
	// DaysLeft is the days remaining in the trial or the paid period (rounded
	// up), and 0 once expired.
	DaysLeft           int        `json:"days_left"`
	TrialEndsAt        *time.Time `json:"trial_ends_at,omitempty"`
	SubscriptionEndsAt *time.Time `json:"subscription_ends_at,omitempty"`
}

// Open reports whether the firm may use the product.
func (a Access) Open() bool { return a.State != AccessExpired }

// ComputeAccess decides the state. A subscription is valid through the whole
// of its last day (the period end is a date) — in Africa/Nairobi, build-
// phases.txt 3f-5's one business calendar, not whatever Location a
// database/sql driver happened to attach when it scanned that DATE column
// (typically midnight UTC): comparing that raw value against "now" at the
// wrong offset silently closes or extends access by a few hours around
// every midnight. now must be moneyfmt.Now(); trialEndsAt and sub may be nil.
func ComputeAccess(now time.Time, trialEndsAt *time.Time, sub *PlatformSubscription) Access {
	out := Access{State: AccessExpired, TrialEndsAt: trialEndsAt}

	if sub != nil {
		end := moneyfmt.AtNairobiMidnight(sub.CurrentPeriodEnd).AddDate(0, 0, 1) // through the end of that day, in Nairobi
		out.SubscriptionEndsAt = &sub.CurrentPeriodEnd
		if (sub.Status == SubscriptionActive || sub.Status == SubscriptionTrialing) && now.Before(end) {
			out.State = AccessSubscribed
			out.DaysLeft = daysUntil(now, end)
			return out
		}
	}
	if trialEndsAt != nil && now.Before(*trialEndsAt) {
		out.State = AccessTrial
		out.DaysLeft = daysUntil(now, *trialEndsAt)
	}
	return out
}

// daysUntil rounds up: 6 hours left is "1 day left", never "0".
func daysUntil(now, end time.Time) int {
	d := end.Sub(now)
	if d <= 0 {
		return 0
	}
	return int((d + 24*time.Hour - 1) / (24 * time.Hour))
}
