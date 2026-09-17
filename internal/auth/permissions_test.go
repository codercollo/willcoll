package auth

import "testing"

func TestAuthorizeMatchesRoleCapabilityTable(t *testing.T) {
	s := NewService(make([]byte, 32), nil)

	cases := []struct {
		role   string
		action Action
		want   bool
	}{
		{"manager", ActionInviteAgent, true},
		{"agent", ActionInviteAgent, false},
		{"landlord", ActionInviteAgent, false},
		{"agent", ActionRecordPayment, true},
		{"landlord", ActionRecordPayment, false},
		{"agent", ActionEditUnitPricing, false}, // "if granted" — PBAC, not base RBAC
		{"manager", ActionEditUnitPricing, true},
		{"landlord", ActionViewLedger, true},
		{"unknown-role", ActionViewPortfolio, false},
	}

	for _, tc := range cases {
		if got := s.Authorize(tc.role, tc.action); got != tc.want {
			t.Errorf("Authorize(%q, %q) = %v, want %v", tc.role, tc.action, got, tc.want)
		}
	}
}
