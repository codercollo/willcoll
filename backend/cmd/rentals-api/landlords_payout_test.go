package main

import (
	"strings"
	"testing"
)

func TestChangedPayoutFields(t *testing.T) {
	s := func(v string) *string { return &v }
	tests := []struct {
		name        string
		fields      []payoutField
		wantChanged bool
		wantTarget  string
		neverIn     string // must not appear in the alert text
	}{
		{"nothing sent", []payoutField{{name: "bank name", now: s("Equity")}}, false, "", ""},
		{"echo is not a change", []payoutField{{name: "bank account number", sent: s("0123456789"), now: s("0123456789"), secret: true}}, false, "", ""},
		{"account number change is masked both ways", []payoutField{{name: "bank account number", sent: s("1111222233"), now: s("0123456789"), secret: true}}, true,
			"Acme: bank account number ****6789 to ****2233", "0123456789"},
		{"set on a landlord that had none", []payoutField{{name: "bank account number", sent: s("1111222233"), secret: true}}, true,
			"Acme: bank account number none to ****2233", "1111222233"},
		{"clearing is a change", []payoutField{{name: "bank account number", sent: s(""), now: s("0123456789"), secret: true}}, true,
			"Acme: bank account number ****6789 to none", ""},
		{"non-secret fields are not shown", []payoutField{{name: "bank name", sent: s("KCB"), now: s("Equity")}}, true, "Acme: bank name changed", "KCB"},
		{"short secret never leaks", []payoutField{{name: "bank account number", sent: s("123"), secret: true}}, true, "Acme: bank account number none to ****", "123"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			changed, target := changedPayoutFields("Acme", tc.fields)
			if changed != tc.wantChanged {
				t.Fatalf("changed = %v, want %v", changed, tc.wantChanged)
			}
			if changed && target != tc.wantTarget {
				t.Errorf("target = %q, want %q", target, tc.wantTarget)
			}
			if tc.neverIn != "" && strings.Contains(target, tc.neverIn) {
				t.Errorf("target %q leaks %q", target, tc.neverIn)
			}
		})
	}
}
