package data

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/codercollo/willcoll/backend/internal/db/sqlc"
)

// What a firm may do, derived from its free trial and its subscription.
//
//	subscribed  an active (or trialing) subscription whose paid period has not ended
//	trial       no live subscription, but the free trial is still running
//	expired     neither: the firm must buy a plan; its dashboard is closed except
//	            for /account and /billing. Tenants can still pay their rent.
const (
	AccessSubscribed = "subscribed"
	AccessTrial      = "trial"
	AccessExpired    = "expired"
)

// DefaultTrialDays is how long the free trial lasts unless TRIAL_DAYS says otherwise.
const DefaultTrialDays = 7

// Access is a firm's standing, sent to the dashboard so it can show the trial
// countdown or the paywall.
type Access struct {
	State string `json:"state"`
	// DaysLeft is the days remaining in the trial or the paid period (rounded
	// up), and 0 once expired.
	DaysLeft           int        `json:"days_left"`
	TrialEndsAt        *time.Time `json:"trial_ends_at,omitempty"`
	SubscriptionEndsAt *time.Time `json:"subscription_ends_at,omitempty"`
}

// Open reports whether the firm may use the dashboard.
func (a Access) Open() bool { return a.State != AccessExpired }

// ComputeAccess decides the state. A subscription is valid through the whole
// of its last day (the period end is a date).
func ComputeAccess(now time.Time, trialEndsAt *time.Time, sub *Subscription) Access {
	out := Access{State: AccessExpired, TrialEndsAt: trialEndsAt}

	if sub != nil {
		end := sub.CurrentPeriodEnd.AddDate(0, 0, 1) // through the end of that day
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

// TrialEnd is when a trial that starts at start ends, days later.
func TrialEnd(start time.Time, days int) time.Time {
	if days <= 0 {
		days = DefaultTrialDays
	}
	return start.Add(time.Duration(days) * 24 * time.Hour)
}

// AccessFor loads the firm's latest subscription and combines it with its trial.
func (m BillingModel) AccessFor(ctx context.Context, manager *Manager) (Access, error) {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	var sub *Subscription
	err := m.Store.ExecTenantTx(ctx, manager.ID, func(q sqlc.Querier) error {
		row, err := q.GetSubscriptionByManager(ctx, manager.ID)
		if errors.Is(err, sql.ErrNoRows) {
			sub = nil
			return nil
		}
		if err != nil {
			return err
		}
		sub = &Subscription{ID: row.ID, PlanID: row.PlanID, Status: row.Status, CurrentPeriodStart: row.CurrentPeriodStart, CurrentPeriodEnd: row.CurrentPeriodEnd}
		return nil
	})
	if err != nil {
		return Access{}, err
	}
	return ComputeAccess(time.Now(), manager.TrialEndsAt, sub), nil
}
