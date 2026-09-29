package reconciliation

import (
	"reflect"
	"testing"

	"github.com/codercollo/willcoll/backend/pkg/moneyfmt"
)

func m(t *testing.T, s string) moneyfmt.Money {
	t.Helper()
	v, err := moneyfmt.Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func open(t *testing.T, pairs ...string) []OpenBalance {
	t.Helper()
	var out []OpenBalance
	for i := 0; i < len(pairs); i += 2 {
		out = append(out, OpenBalance{LedgerType: pairs[i], Balance: m(t, pairs[i+1])})
	}
	return out
}

func types(combos [][]Allocation) [][]string {
	var out [][]string
	for _, c := range combos {
		var ts []string
		for _, a := range c {
			ts = append(ts, a.LedgerType)
		}
		out = append(out, ts)
	}
	return out
}

func TestFindMatchingCombinations(t *testing.T) {
	tests := []struct {
		name   string
		amount string
		open   []OpenBalance
		want   [][]string
	}{
		{"exactly one combination", "6000", open(t, Rent, "5000", Water, "1000", Garbage, "300"), [][]string{{Water, Rent}}},
		{"a single balance", "300", open(t, Rent, "5000", Water, "1000", Garbage, "300"), [][]string{{Garbage}}},
		{"everything owed", "6300", open(t, Rent, "5000", Water, "1000", Garbage, "300"), [][]string{{Water, Garbage, Rent}}},
		{"no combination", "1234", open(t, Rent, "5000", Water, "1000"), nil},
		{"ambiguous: two combinations", "1000", open(t, Rent, "1000", Water, "1000"), [][]string{{Water}, {Rent}}},
		{"ambiguous by sum", "1500", open(t, Water, "500", Garbage, "1000", Rent, "1500"), [][]string{{Water, Garbage}, {Rent}}},
		{"zero and credit balances are not open", "500", open(t, Water, "0", Rent, "-200", Garbage, "500"), [][]string{{Garbage}}},
		{"duplicate types merge", "300", open(t, Rent, "100", Rent, "200"), [][]string{{Rent}}},
		{"non-positive payment", "0", open(t, Rent, "100"), nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := types(FindMatchingCombinations(m(t, tc.amount), tc.open))
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("FindMatchingCombinations = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestFindMatchingCombinationsAllocationsAreFullBalances(t *testing.T) {
	got := FindMatchingCombinations(m(t, "6000"), open(t, Rent, "5000", Water, "1000"))
	want := [][]Allocation{{{Water, m(t, "1000")}, {Rent, m(t, "5000")}}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestApplyWaterfall(t *testing.T) {
	tests := []struct {
		name   string
		amount string
		open   []OpenBalance
		want   []Allocation
	}{
		{"pays in the default order", "1500", open(t, Rent, "5000", Water, "1000", Garbage, "300"),
			[]Allocation{{Water, m(t, "1000")}, {Garbage, m(t, "300")}, {Rent, m(t, "200")}}},
		{"stops when the money runs out", "700", open(t, Rent, "5000", Water, "1000"),
			[]Allocation{{Water, m(t, "700")}}},
		{"all deposits after rent", "9000", open(t, WaterDeposit, "1000", RentDeposit, "3000", Rent, "5000"),
			[]Allocation{{Rent, m(t, "5000")}, {RentDeposit, m(t, "3000")}, {WaterDeposit, m(t, "1000")}}},
		{"overpayment becomes advance rent on the existing rent line", "6500", open(t, Rent, "5000", Water, "1000"),
			[]Allocation{{Water, m(t, "1000")}, {Rent, m(t, "5500")}}},
		{"overpayment with only water owed adds a rent line", "1500", open(t, Water, "1000"),
			[]Allocation{{Water, m(t, "1000")}, {Rent, m(t, "500")}}},
		{"nothing owed: the whole payment is advance rent", "2000", nil,
			[]Allocation{{Rent, m(t, "2000")}}},
		{"non-positive payment", "0", open(t, Rent, "100"), nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ApplyWaterfall(m(t, tc.amount), tc.open)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("ApplyWaterfall = %+v, want %+v", got, tc.want)
			}
			var sum moneyfmt.Money
			for _, a := range got {
				sum = sum.Add(a.Amount)
			}
			if len(got) > 0 && sum != m(t, tc.amount) {
				t.Errorf("allocations sum to %s, want exactly %s", sum, tc.amount)
			}
		})
	}
}

func TestNormalizePhone(t *testing.T) {
	for in, want := range map[string]string{
		"0722 000 000":     "+254722000000",
		"722000000":        "+254722000000",
		"254722000000":     "+254722000000",
		"+254 722-000-000": "+254722000000",
		"0110123456":       "+254110123456",
		"":                 "",
		"12345":            "12345",
	} {
		if got := NormalizePhone(in); got != want {
			t.Errorf("NormalizePhone(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMatchUnit(t *testing.T) {
	cands := []Candidate{
		{UnitID: "A1", Phones: []string{"0722000001", "+254733000001"}, Names: []string{"John Kamau", "Mary Wanjiku"}},
		{UnitID: "A2", Phones: []string{"0722000002"}, Names: []string{"Acme Traders Ltd"}},
		{UnitID: "A3", Phones: []string{"0722000003"}, Names: []string{"John Otieno"}},
		{UnitID: "A4", Phones: []string{"0722000001"}, Names: []string{"Peter Njoroge"}}, // shares A1's phone
	}
	tests := []struct {
		name          string
		msisdn, payer string
		wantUnit      string
		wantMethod    Method
		wantAmbiguous bool
	}{
		{"phone in any format", "254722000002", "", "A2", ByPhone, false},
		{"a co-payer phone", "0733 000 001", "", "A1", ByPhone, false},
		{"phone shared by two units is ambiguous", "0722000001", "", "", ByPhone, true},
		{"phone unknown, name with an extra middle name", "0799999999", "JOHN KAMAU MWANGI", "A1", ByName, false},
		{"a co-payer name", "0799999999", "WANJIKU MARY", "A1", ByName, false},
		{"first name alone is never enough", "0799999999", "JOHN", "", "", false},
		{"one shared token is not enough", "0799999999", "JOHN NDUNGU", "", "", false},
		{"company name", "0799999999", "ACME TRADERS", "A2", ByName, false},
		{"no phone, no name", "", "", "", "", false},
		{"phone wins over a name for another unit", "0722000003", "JOHN KAMAU", "A3", ByPhone, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := MatchUnit(tc.msisdn, tc.payer, cands)
			if got.UnitID != tc.wantUnit || got.Ambiguous != tc.wantAmbiguous || (tc.wantMethod != "" && got.Method != tc.wantMethod) {
				t.Errorf("MatchUnit = %+v, want unit %q method %q ambiguous %v", got, tc.wantUnit, tc.wantMethod, tc.wantAmbiguous)
			}
			if got.Found() != (tc.wantUnit != "") {
				t.Errorf("Found() = %v", got.Found())
			}
		})
	}
}

func TestMatchUnitAmbiguousNames(t *testing.T) {
	cands := []Candidate{
		{UnitID: "B1", Names: []string{"Grace Wambui Kariuki"}},
		{UnitID: "B2", Names: []string{"Grace Wambui Njeri"}},
	}
	got := MatchUnit("", "GRACE WAMBUI", cands)
	if !got.Ambiguous || got.UnitID != "" {
		t.Errorf("two equally good units must be ambiguous, got %+v", got)
	}
}
