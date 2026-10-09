package data

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/codercollo/willcoll/backend/internal/rentals/pdf"
)

// D6 (docs/security/authorization.md): a viewer reads the monthly report on
// screen. What the report carries is names, house numbers, amounts and dates the
// viewer role already reads elsewhere; it carries no phone number, so there is
// nothing to mask. This pins that: a field named like contact data being added to
// the report's JSON fails here, and has to be masked for the viewer (or kept off
// the response) before it ships.
func TestReportPayloadCarriesNoContactData(t *testing.T) {
	sample := []any{
		MonthlyReport{
			Rows:  []pdf.ScheduleRow{{HouseNo: "A1", Tenants: []string{"Jane"}, Rent: []pdf.SchedulePayment{{}}}},
			Note1: []string{"x"}, Notes: []string{"y"},
		},
		SanityResult{Failures: []SanityFailure{{}}, Info: []string{"z"}},
	}
	for _, v := range sample {
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		var generic any
		if err := json.Unmarshal(raw, &generic); err != nil {
			t.Fatal(err)
		}
		walkKeys(generic, func(key string) {
			k := strings.ToLower(key)
			for _, bad := range []string{"phone", "msisdn", "email", "mobile", "tel"} {
				if k == bad || strings.Contains(k, bad+"_") || strings.HasSuffix(k, "_"+bad) {
					t.Errorf("%T carries a %q field: mask it for the viewer role before returning it (authorization.md D6)", v, key)
				}
			}
		})
	}
}

func walkKeys(v any, fn func(string)) {
	switch x := v.(type) {
	case map[string]any:
		for k, child := range x {
			fn(k)
			walkKeys(child, fn)
		}
	case []any:
		for _, child := range x {
			walkKeys(child, fn)
		}
	}
}
