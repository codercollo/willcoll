package pdf

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/codercollo/willcoll/backend/pkg/moneyfmt"
)

var tjRe = regexp.MustCompile(`\((.*)\) Tj`)

// pdfText returns every text run in an uncompressed maroto PDF, one per line.
func pdfText(t *testing.T, b []byte) string {
	t.Helper()
	if !strings.HasPrefix(string(b), "%PDF") {
		t.Fatal("output is not a PDF")
	}
	var out []string
	for _, m := range tjRe.FindAllStringSubmatch(string(b), -1) {
		s := strings.NewReplacer(`\(`, "(", `\)`, ")", `\`, `\`).Replace(m[1])
		out = append(out, s)
	}
	return strings.Join(out, "\n")
}

func money(t *testing.T, s string) moneyfmt.Money {
	t.Helper()
	m, err := moneyfmt.Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func mustContain(t *testing.T, text string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(text, w) {
			t.Errorf("PDF text missing %q\n---\n%s", w, text)
		}
	}
}

func mustNotContain(t *testing.T, text string, unwanted ...string) {
	t.Helper()
	low := strings.ToLower(text)
	for _, u := range unwanted {
		if strings.Contains(low, strings.ToLower(u)) {
			t.Errorf("PDF text unexpectedly contains %q", u)
		}
	}
}

var (
	sep   = time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	theme = Theme{HeaderText: "KIWI PLACE", AddressLines: []string{"P.O. Box 123-00100, Nairobi"}, Phone: "0700 000 000"}
	payTo = PaymentParticulars{AccountName: "KIWI PLACE", AccountNumber: "522522", BankName: "KCB"}
	d     = func(day int) time.Time { return time.Date(2026, 9, day, 0, 0, 0, 0, time.UTC) }
)

func TestBuildReceipt(t *testing.T) {
	tests := []struct {
		name string
		r    Receipt
		want []string
	}{
		{"single payer, rent only", Receipt{
			Theme: theme, PropertyName: "KIWI PLACE", ReceiptNo: 1042, Date: d(5), Period: sep,
			ReceivedFrom: []string{"JOHN KAMAU"}, HouseNo: "A4", Method: MethodMpesa, MpesaCode: "QWE123",
			LedgerType: LedgerRent, Amount: money(t, "12000"),
		}, []string{"KIWI PLACE", "1042", "JOHN KAMAU", "A4", "12,000", "QWE123"}},
		{"co-payers, rent deposit, cash", Receipt{
			Theme: theme, PropertyName: "KIWI PLACE", ReceiptNo: 7, Date: d(5), Period: sep,
			ReceivedFrom: []string{"JOHN KAMAU", "MARY WANJIKU"}, HouseNo: "B1", Method: MethodCash,
			LedgerType: LedgerRentDeposit, Amount: money(t, "10000"),
		}, []string{"JOHN KAMAU", "MARY WANJIKU", "OR", "10,000"}},
		{"arrears cleared", Receipt{
			Theme: theme, PropertyName: "KIWI PLACE", ReceiptNo: 8, Date: d(5), Period: sep,
			ReceivedFrom: []string{"PETER OMONDI"}, HouseNo: "C2", Method: MethodMpesa,
			LedgerType: LedgerRent, Amount: money(t, "24000"), ArrearsNote: "ARREARS CLEARED: KSH. 12,000.00",
		}, []string{"Arrears", "ARREARS CLEARED"}},
		{"voided", Receipt{
			Theme: theme, PropertyName: "KIWI PLACE", ReceiptNo: 9, Date: d(5), Period: sep,
			ReceivedFrom: []string{"JANE DOE"}, HouseNo: "D1", Method: MethodCash,
			LedgerType: LedgerRent, Amount: money(t, "5000"), Void: true, VoidReason: "duplicate",
		}, []string{"VOID", "DUPLICATE"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			b, err := BuildReceipt(tc.r)
			if err != nil {
				t.Fatal(err)
			}
			mustContain(t, pdfText(t, b), tc.want...)
		})
	}
}

// TestReceiptFixesRegression is the 2026-09-28 receipt bug list: no
// confirmation stamp, "ONLY" exactly once, whole shillings with no ".00",
// the receipt number on the same in-box line as "No.", and a carried-over
// arrears allocation naming the month it actually settled.
func TestReceiptFixesRegression(t *testing.T) {
	r := Receipt{
		Theme: theme, PropertyName: "KIWI PLACE", ReceiptNo: 26, Date: d(3), Period: sep,
		ReceivedFrom: []string{"EUNICE WANJIRU"}, HouseNo: "A1", Method: MethodMpesa, MpesaCode: "QWE1042",
		LedgerType: LedgerRent, Amount: money(t, "9000"),
	}
	b, err := BuildReceipt(r)
	if err != nil {
		t.Fatal(err)
	}
	text := pdfText(t, b)

	mustContain(t, text, "NINE THOUSAND SHILLINGS ONLY.", "No. 26", "9,000/=")
	if n := strings.Count(strings.ToUpper(text), "ONLY"); n != 1 {
		t.Errorf(`"ONLY" appears %d times, want exactly 1:`+"\n%s", n, text)
	}
	mustNotContain(t, text, "9,000.00", "Confirmed", "Period:")

	july := time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC)
	arrears := r
	arrears.SettledPeriod = &july
	arrears.ArrearsNote = "ARREARS CLEARED: KSH. 9,000.00"
	b2, err := BuildReceipt(arrears)
	if err != nil {
		t.Fatal(err)
	}
	text2 := pdfText(t, b2)
	mustContain(t, text2, "JULY 2026 RENT (ARREARS)")
	mustNotContain(t, text2, "SEPTEMBER 2026 RENT:")
}

// TestReceiptVoidSuffixIsASCII guards against mojibake: no em-dash or other
// non-ASCII character anywhere in a rendered receipt.
func TestReceiptVoidSuffixIsASCII(t *testing.T) {
	r := Receipt{
		Theme: theme, PropertyName: "KIWI PLACE", ReceiptNo: 9, Date: d(5), Period: sep,
		ReceivedFrom: []string{"JANE DOE"}, HouseNo: "D1", Method: MethodCash,
		LedgerType: LedgerRent, Amount: money(t, "5000"), Void: true, VoidReason: "duplicate",
	}
	b, err := BuildReceipt(r)
	if err != nil {
		t.Fatal(err)
	}
	text := pdfText(t, b)
	for _, c := range text {
		if c > 127 {
			t.Errorf("non-ASCII rune %q in receipt text:\n%s", c, text)
			break
		}
	}
}

func TestBuildWaterInvoice(t *testing.T) {
	base := WaterInvoice{
		Theme: theme, CustomerName: "JOHN KAMAU", HouseNo: "A4", Period: sep, ReadingDate: d(28),
		CurrentReading: money(t, "150"), PreviousReading: money(t, "140"), UnitsConsumed: money(t, "10"),
		Rate: money(t, "100"), Amount: money(t, "1000"), Payment: payTo,
	}
	tests := []struct {
		name   string
		mutate func(*WaterInvoice)
		want   []string
	}{
		{"no arrears", func(*WaterInvoice) {}, []string{"JOHN KAMAU", "A4", "1,000", "522522", "KCB"}},
		{"carried-forward balance", func(w *WaterInvoice) { w.PriorBalance = money(t, "2500") }, []string{"2,500", "3,500"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			inv := base
			tc.mutate(&inv)
			b, err := BuildWaterInvoice(inv)
			if err != nil {
				t.Fatal(err)
			}
			text := pdfText(t, b)
			mustContain(t, text, tc.want...)
			mustNotContain(t, text, "garbage collection fee")
		})
	}
}

func TestBuildGarbageInvoice(t *testing.T) {
	inv := GarbageInvoice{
		Theme: theme, CustomerName: "JOHN KAMAU", HouseNo: "A4", Period: sep, BillDate: d(28),
		Fee: money(t, "300"), PriorBalance: money(t, "300"), Payment: payTo,
	}
	b, err := BuildGarbageInvoice(inv)
	if err != nil {
		t.Fatal(err)
	}
	text := pdfText(t, b)
	mustContain(t, text, "Garbage Collection Fee", "600", "JOHN KAMAU")
	mustNotContain(t, text, "meter", "cubic")
}

func scheduleFixture(t *testing.T, garbage bool) MonthlySchedule {
	pay := func(day int, amt string) SchedulePayment { return SchedulePayment{Date: d(day), Amount: money(t, amt)} }
	return MonthlySchedule{
		Theme: theme, PropertyName: "The Rundas Arcade", Location: "Nairobi", Period: sep,
		GarbageEnabled: garbage, ConfirmedAt: d(30),
		Note1: []string{"HOUSES OCCUPIED: 2", "VACANT HOUSES: 1", "TOTAL WATER UNITS CONSUMED BY TENANTS: 20"},
		Rows: []ScheduleRow{
			{HouseNo: "G1", Tenants: []string{"JOHN KAMAU", "MARY WANJIKU"},
				Rent:  []SchedulePayment{pay(3, "5000"), pay(17, "2500")},
				Water: []SchedulePayment{pay(3, "1000")}, Garbage: []SchedulePayment{pay(3, "300")}},
			{HouseNo: "SHOP NO.2", Tenants: []string{"PETER OMONDI"}, Rent: []SchedulePayment{pay(9, "8000")}},
		},
		Notes:         []string{"G1 has paid advance for October", "Shop 2 arrears commitment letter received"},
		ManagementFee: ManagementFee{Percent: "10", Fee: money(t, "1650")},
	}
}

func TestBuildMonthlySchedule(t *testing.T) {
	t.Run("multi-payment cell and co-payers", func(t *testing.T) {
		b, err := BuildMonthlySchedule(scheduleFixture(t, false))
		if err != nil {
			t.Fatal(err)
		}
		text := pdfText(t, b)
		mustContain(t, text,
			"THE RUNDAS ARCADE", "ALL IN ONE PAYMENTS SCHEDULE: MONTH: SEPTEMBER 2026",
			"NOTE: 1", "NOTE: 2", "JOHN KAMAU", "OR", "MARY WANJIKU",
			"3/9/2026", "17/9/2026", "5,000", "2,500", "SHOP", "NO.2",
			"G1 has paid advance for October", "MANAGEMENT FEE: 10/100 X KSH. 15,500 = KSHS. 1,650",
			"TOTAL AMOUNTS PAID", "GRAND TOTAL", "PAYMENT.")
	})
	t.Run("garbage disabled omits column", func(t *testing.T) {
		b, err := BuildMonthlySchedule(scheduleFixture(t, false))
		if err != nil {
			t.Fatal(err)
		}
		mustNotContain(t, pdfText(t, b), "GARBAGE")
	})
	t.Run("garbage enabled shows column", func(t *testing.T) {
		b, err := BuildMonthlySchedule(scheduleFixture(t, true))
		if err != nil {
			t.Fatal(err)
		}
		mustContain(t, pdfText(t, b), "GARBAGE PAID")
	})
}

func TestBuildSubscriptionInvoice(t *testing.T) {
	b, err := BuildSubscriptionInvoice(SubscriptionInvoice{InvoiceNo: "INV-9", ManagerName: "Acme Ltd",
		Period: sep, IssuedAt: d(1), Amount: money(t, "4500")})
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, pdfText(t, b), "WILLCOLL", "INV-9", "Acme Ltd", "SEPTEMBER 2026", "4,500")
}

func TestThemeFallbackRenders(t *testing.T) {
	b, err := BuildWaterInvoice(WaterInvoice{CustomerName: "X", HouseNo: "1", Period: sep, ReadingDate: d(1)})
	if err != nil || len(b) == 0 {
		t.Fatalf("empty theme must still render: %v", err)
	}
}

var pageRe = regexp.MustCompile(`/Type\s*/Page\b`)

func pageCount(b []byte) int { return len(pageRe.FindAll(b, -1)) }

// A bill or receipt is exactly one page: no footer spilling onto a second page
// and no blank page between documents (both happened before addPages).
func TestOnePagePerDocument(t *testing.T) {
	const n = 4
	// The fullest header the theme allows: four address lines and a phone.
	full := Theme{HeaderText: "KIWI PLACE", AddressLines: []string{"P.O. Box 123-00100", "Kiwi Road, Kasarani", "Nairobi, Kenya", "Second floor, Block B"}, Phone: "0700 000 000"}

	fullReceipt := Receipt{
		Theme: full, PropertyName: "KIWI PLACE", ReceiptNo: 1, Date: d(5), Period: sep,
		ReceivedFrom: []string{"JOHN KAMAU", "MARY WANJIKU", "PETER OMONDI"}, HouseNo: "SHOP NO.2", Method: MethodMpesa,
		MpesaCode: "QWE123", LedgerType: LedgerRent, Amount: money(t, "12000"),
		ArrearsNote: "ARREARS CLEARED: KSH. 3,000.00",
	}
	receipts := make([]Receipt, n)
	waters := make([]WaterInvoice, n)
	garbages := make([]GarbageInvoice, n)
	for i := range n {
		receipts[i] = fullReceipt
		receipts[i].ReceiptNo = int64(i + 1)
		waters[i] = WaterInvoice{Theme: full, CustomerName: "JOHN KAMAU", HouseNo: "A4", Period: sep, ReadingDate: d(28),
			CurrentReading: money(t, "150"), PreviousReading: money(t, "140"), UnitsConsumed: money(t, "10"),
			Rate: money(t, "100"), Amount: money(t, "1000"), PriorBalance: money(t, "2500"), Payment: payTo}
		garbages[i] = GarbageInvoice{Theme: full, CustomerName: "JOHN KAMAU", HouseNo: "A4", Period: sep, BillDate: d(28),
			Fee: money(t, "300"), PriorBalance: money(t, "300"), Payment: payTo}
	}

	for name, build := range map[string]func() ([]byte, error){
		"receipts":         func() ([]byte, error) { return BuildReceipts(receipts) },
		"water invoices":   func() ([]byte, error) { return BuildWaterInvoices(waters) },
		"garbage invoices": func() ([]byte, error) { return BuildGarbageInvoices(garbages) },
	} {
		t.Run(name, func(t *testing.T) {
			b, err := build()
			if err != nil {
				t.Fatal(err)
			}
			if got := pageCount(b); got != n {
				t.Errorf("%d documents rendered as %d pages, want %d", n, got, n)
			}
		})
	}
}

func TestWrapWords(t *testing.T) {
	for _, tc := range []struct {
		in   string
		max  int
		want []string
	}{
		{"G1", 7, []string{"G1"}},
		{"SHOP NO.1", 7, []string{"SHOP", "NO.1"}},
		{"MAMA MBOGA ENTERPRISES", 24, []string{"MAMA MBOGA ENTERPRISES"}},
		{"KIWI SALON & SPA LIMITED KENYA", 24, []string{"KIWI SALON & SPA LIMITED", "KENYA"}},
		{"SUPERCALIFRAGILISTICEXPIALIDOCIOUS", 7, []string{"SUPERCALIFRAGILISTICEXPIALIDOCIOUS"}},
	} {
		if got := wrapWords(tc.in, tc.max); strings.Join(got, "|") != strings.Join(tc.want, "|") {
			t.Errorf("wrapWords(%q, %d) = %q, want %q", tc.in, tc.max, got, tc.want)
		}
	}
}
