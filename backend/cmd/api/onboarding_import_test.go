package main

import (
	"strings"
	"testing"
	"time"

	"github.com/codercollo/willcoll/backend/internal/data"
)

var asAtOct = time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)

func problemsAt(ps []data.ImportRowError, row int, field string) []data.ImportRowError {
	var out []data.ImportRowError
	for _, p := range ps {
		if p.Row == row && p.Field == field {
			out = append(out, p)
		}
	}
	return out
}

func TestParseOnboardCSV(t *testing.T) {
	t.Run("a full sheet: tenants, deposits, arrears, a co-payer and a vacant unit", func(t *testing.T) {
		csv := "unit_code,meter_number,tenant_name,phone,rent,start_date,rent_deposit,water_deposit,rent_arrears,water_arrears,garbage_arrears,co_payer_name,co_payer_phone\n" +
			"G1,W-1,JOHN KAMAU,0722 000 001,\"18,000\",2025-07-01,36000,3000,5000,700,300,MARY WANJIKU,254733000002\n" +
			"G2,W-2,,,,,,,,,,,\n"
		rows, problems := parseOnboardCSV([]byte(csv), asAtOct)
		if len(problems) != 0 || len(rows) != 2 {
			t.Fatalf("rows %+v problems %+v", rows, problems)
		}
		g1 := rows[0]
		if g1.Phone != "+254722000001" || g1.Rent.String() != "18000.00" || g1.RentDeposit.String() != "36000.00" || g1.GarbageArrears.String() != "300.00" ||
			g1.StartDate == nil || g1.StartDate.Format("2006-01-02") != "2025-07-01" || g1.MeterNumber == nil || *g1.MeterNumber != "W-1" {
			t.Errorf("G1 = %+v", g1)
		}
		if g1.CoPayerName == nil || *g1.CoPayerName != "MARY WANJIKU" || g1.CoPayerPhone == nil || *g1.CoPayerPhone != "+254733000002" {
			t.Errorf("co-payer = %v %v", g1.CoPayerName, g1.CoPayerPhone)
		}
		if rows[1].HasTenant() || rows[1].UnitCode != "G2" || rows[1].Row != 3 {
			t.Errorf("G2 should be a vacant unit on row 3: %+v", rows[1])
		}
	})

	t.Run("only unit_code is required; other columns can be left out entirely", func(t *testing.T) {
		rows, problems := parseOnboardCSV([]byte("unit_code\nA1\nA2\n"), asAtOct)
		if len(problems) != 0 || len(rows) != 2 || rows[0].HasTenant() {
			t.Fatalf("rows %+v problems %+v", rows, problems)
		}
	})

	t.Run("accepts natural header spellings, any order, a BOM and blank lines, and counts real file rows", func(t *testing.T) {
		csv := "\xef\xbb\xbfHouse No,Tenant,Mobile,Monthly Rent\nA1,JOHN,0711222333,9000\n\nA2,JANE,0711222444,9500\n"
		rows, problems := parseOnboardCSV([]byte(csv), asAtOct)
		if len(problems) != 0 || len(rows) != 2 || rows[0].Phone != "+254711222333" || rows[1].Row != 4 {
			t.Fatalf("rows %+v problems %+v (row numbers must count the blank line)", rows, problems)
		}
	})

	t.Run("phones are normalised from the ways they are written", func(t *testing.T) {
		for in, want := range map[string]string{"0722 000 001": "+254722000001", "0722000001": "+254722000001", "254722000001": "+254722000001",
			"+254 722 000 001": "+254722000001", "0111-222-333": "+254111222333"} {
			if got, ok := normalizeKenyanPhone(in); !ok || got != want {
				t.Errorf("normalizeKenyanPhone(%q) = %q, %v; want %q", in, got, ok, want)
			}
		}
		for _, in := range []string{"", "12345", "0622000001", "+255722000001", "07220000"} {
			if _, ok := normalizeKenyanPhone(in); ok {
				t.Errorf("normalizeKenyanPhone(%q) was accepted", in)
			}
		}
	})

	t.Run("amounts are read as written on paper, and dates day-first", func(t *testing.T) {
		for in, want := range map[string]string{"18,000": "18000.00", "Ksh 18000": "18000.00", "KES 1 500.50": "1500.50", "0": "0.00"} {
			if m, err := parseLenientMoney(in); err != nil || m.String() != want {
				t.Errorf("parseLenientMoney(%q) = %v, %v; want %s", in, m, err, want)
			}
		}
		if _, err := parseLenientMoney("12.345"); err == nil {
			t.Error("three decimals were accepted")
		}
		for _, in := range []string{"2025-07-01", "01/07/2025", "1/7/2025", "01-07-2025"} {
			if d, err := parseFlexibleDate(in); err != nil || d.Format("2006-01-02") != "2025-07-01" {
				t.Errorf("parseFlexibleDate(%q) = %v, %v", in, d, err)
			}
		}
	})

	bad := map[string]struct {
		csv          string
		row          int
		field        string
		messageHas   string
		wantAtLeast1 bool
	}{
		"unknown column":                {"unit_code,floor\nA1,2\n", 1, "floor", "not a known column", true},
		"no unit_code column":           {"tenant_name\nJOHN\n", 1, "unit_code", "must have a unit_code", true},
		"a repeated column":             {"unit_code,unit\nA1,A1\n", 1, "unit_code", "twice", true},
		"a repeated unit":               {"unit_code\nA1\nA2\nA1\n", 4, "unit_code", "repeats row 2", true},
		"tenant with no phone":          {"unit_code,tenant_name,rent\nA1,JOHN,5000\n", 2, "phone", "must be provided", true},
		"tenant with a bad phone":       {"unit_code,tenant_name,phone,rent\nA1,JOHN,12345,5000\n", 2, "phone", "Kenyan number", true},
		"tenant with no rent":           {"unit_code,tenant_name,phone\nA1,JOHN,0722000001\n", 2, "rent", "must be provided", true},
		"zero rent":                     {"unit_code,tenant_name,phone,rent\nA1,JOHN,0722000001,0\n", 2, "rent", "greater than 0", true},
		"a negative arrears":            {"unit_code,tenant_name,phone,rent,rent_arrears\nA1,JOHN,0722000001,5000,-100\n", 2, "rent_arrears", "negative", true},
		"a nonsense amount":             {"unit_code,tenant_name,phone,rent,water_deposit\nA1,JOHN,0722000001,5000,abc\n", 2, "water_deposit", "amount like", true},
		"a future start date":           {"unit_code,tenant_name,phone,rent,start_date\nA1,JOHN,0722000001,5000,2027-01-01\n", 2, "start_date", "after the as-at", true},
		"a bad date":                    {"unit_code,tenant_name,phone,rent,start_date\nA1,JOHN,0722000001,5000,soon\n", 2, "start_date", "date like", true},
		"half a tenant on a vacant row": {"unit_code,tenant_name,rent\nA1,,5000\n", 2, "rent", "tenant_name is empty", true},
		"co-payer phone without a name": {"unit_code,tenant_name,phone,rent,co_payer_phone\nA1,JOHN,0722000001,5000,0733000002\n", 2, "co_payer_name", "must be provided", true},
		"co-payer is the tenant":        {"unit_code,tenant_name,phone,rent,co_payer_name,co_payer_phone\nA1,JOHN,0722000001,5000,MARY,0722000001\n", 2, "co_payer_phone", "same as the tenant", true},
		"a header and no rows":          {"unit_code\n", 2, "unit_code", "no units", true},
	}
	for name, tc := range bad {
		t.Run(name, func(t *testing.T) {
			rows, problems := parseOnboardCSV([]byte(tc.csv), asAtOct)
			if rows != nil || len(problems) == 0 {
				t.Fatalf("accepted: %+v", rows)
			}
			got := problemsAt(problems, tc.row, tc.field)
			if len(got) == 0 || !strings.Contains(got[0].Message, tc.messageHas) {
				t.Errorf("want a problem on row %d %q containing %q; got %+v", tc.row, tc.field, tc.messageHas, problems)
			}
		})
	}

	t.Run("every problem is reported at once", func(t *testing.T) {
		_, problems := parseOnboardCSV([]byte("unit_code,tenant_name,phone,rent\nA1,JOHN,999,0\nA1,MARY,0722000001,5000\n,PETER,0722000002,4000\n"), asAtOct)
		if len(problems) < 4 {
			t.Errorf("only %d problems reported: %+v (want the phone, the rent, the repeat and the missing unit together)", len(problems), problems)
		}
	})

	t.Run("more than 500 rows is refused", func(t *testing.T) {
		var b strings.Builder
		b.WriteString("unit_code\n")
		for i := 0; i < 501; i++ {
			b.WriteString("U" + itoa(i) + "\n")
		}
		if _, problems := parseOnboardCSV([]byte(b.String()), asAtOct); len(problems) == 0 || !strings.Contains(problems[len(problems)-1].Message, "more than 500") {
			t.Errorf("problems = %+v", problems)
		}
	})
}
