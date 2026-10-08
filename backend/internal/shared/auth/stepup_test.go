package auth

import (
	"testing"
	"time"
)

func TestStepUpWindow(t *testing.T) {
	opened := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	until := StepUpUntil(opened)

	cases := []struct {
		name  string
		until int64
		now   time.Time
		want  bool
	}{
		{"never stepped up", 0, opened, false},
		{"just opened", until, opened, true},
		{"one second before expiry", until, opened.Add(StepUpTTL - time.Second), true},
		{"at expiry", until, opened.Add(StepUpTTL), false},
		{"after expiry", until, opened.Add(StepUpTTL + time.Minute), false},
	}
	for _, tc := range cases {
		if got := StepUpActive(tc.until, tc.now); got != tc.want {
			t.Errorf("%s: StepUpActive = %v, want %v", tc.name, got, tc.want)
		}
	}
	if StepUpTTL != 5*time.Minute {
		t.Errorf("StepUpTTL = %v, D5 approved 5 minutes", StepUpTTL)
	}
}
