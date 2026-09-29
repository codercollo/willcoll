package data

import (
	"testing"
	"time"
)

func TestComputeAccess(t *testing.T) {
	now := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	at := func(d time.Duration) *time.Time { x := now.Add(d); return &x }
	day := func(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }
	sub := func(status string, end time.Time) *Subscription {
		return &Subscription{Status: status, CurrentPeriodStart: end.AddDate(0, -1, 0), CurrentPeriodEnd: end}
	}

	tests := []struct {
		name     string
		trial    *time.Time
		sub      *Subscription
		want     string
		daysLeft int
	}{
		{"never activated", nil, nil, AccessExpired, 0},
		{"just started a 7 day trial", at(7 * 24 * time.Hour), nil, AccessTrial, 7},
		{"six hours left rounds up to a day, never zero", at(6 * time.Hour), nil, AccessTrial, 1},
		{"trial ended a minute ago", at(-time.Minute), nil, AccessExpired, 0},
		{"paid, period ends next month", at(-24 * time.Hour), sub(SubscriptionActive, day(2026, 10, 26)), AccessSubscribed, 31},
		{"paid period ends today: valid through the whole day", nil, sub(SubscriptionActive, day(2026, 9, 26)), AccessSubscribed, 1},
		{"paid period ended yesterday", nil, sub(SubscriptionActive, day(2026, 9, 25)), AccessExpired, 0},
		{"past due is not access, even mid-period", nil, sub(SubscriptionPastDue, day(2026, 10, 26)), AccessExpired, 0},
		{"cancelled is not access", nil, sub(SubscriptionCancelled, day(2026, 10, 26)), AccessExpired, 0},
		{"lapsed subscription but the trial still runs", at(3 * 24 * time.Hour), sub(SubscriptionActive, day(2026, 9, 1)), AccessTrial, 3},
		{"subscribed wins over a running trial", at(3 * 24 * time.Hour), sub(SubscriptionActive, day(2026, 10, 26)), AccessSubscribed, 31},
		{"a trialing subscription counts", nil, sub(SubscriptionTrialing, day(2026, 10, 3)), AccessSubscribed, 8},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ComputeAccess(now, tc.trial, tc.sub)
			if got.State != tc.want || got.DaysLeft != tc.daysLeft {
				t.Errorf("state %q, %d days left; want %q, %d", got.State, got.DaysLeft, tc.want, tc.daysLeft)
			}
			if got.Open() != (tc.want != AccessExpired) {
				t.Errorf("Open() = %v for state %q", got.Open(), got.State)
			}
		})
	}
}

func TestTrialEnd(t *testing.T) {
	start := time.Date(2026, 9, 26, 10, 0, 0, 0, time.UTC)
	if got := TrialEnd(start, 7); !got.Equal(start.Add(7 * 24 * time.Hour)) {
		t.Errorf("TrialEnd(7) = %v", got)
	}
	if got := TrialEnd(start, 0); !got.Equal(start.Add(DefaultTrialDays * 24 * time.Hour)) {
		t.Errorf("a zero setting must fall back to the default, got %v", got)
	}
}
