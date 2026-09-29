package pdf

import (
	"fmt"
	"strings"
	"time"

	"github.com/codercollo/willcoll/backend/pkg/moneyfmt"
	"github.com/johnfercher/maroto/v2/pkg/components/col"
	"github.com/johnfercher/maroto/v2/pkg/components/line"
	"github.com/johnfercher/maroto/v2/pkg/components/row"
	"github.com/johnfercher/maroto/v2/pkg/components/text"
	"github.com/johnfercher/maroto/v2/pkg/consts/align"
	"github.com/johnfercher/maroto/v2/pkg/consts/fontstyle"
	"github.com/johnfercher/maroto/v2/pkg/consts/pagesize"
	"github.com/johnfercher/maroto/v2/pkg/core"
	"github.com/johnfercher/maroto/v2/pkg/props"
)

// MonthlySchedule is the "ALL IN ONE PAYMENTS SCHEDULE" landlord report for
// one property and period. It renders only from a confirmed period's frozen
// snapshot (system-design.txt PDF confirmation addendum): Note1 and Notes
// are printed verbatim, never recomputed here.
type MonthlySchedule struct {
	Theme          Theme
	PropertyName   string
	Location       string
	Period         time.Time
	GarbageEnabled bool // false omits the garbage column entirely (system-design 7.9)
	// ElectricityDepositShown: false omits the electricity deposit column
	// entirely — same "column simply doesn't exist" rule as garbage, not a
	// blank/zero column.
	ElectricityDepositShown bool
	Note1                   []string // "NOTE: 1", frozen at confirmation, printed verbatim
	Rows                    []ScheduleRow
	Notes                   []string // "NOTE: 2", frozen at confirmation, printed verbatim
	ManagementFee           ManagementFee
	ConfirmedAt             time.Time
}

// ScheduleSummary is the raw NOTE:1 numbers, kept for the JSON preview
// (data.MonthlyReport.Summary) and the confirmation gate's calculations —
// the PDF itself renders Note1's frozen, pre-rendered lines instead.
type ScheduleSummary struct {
	Occupied      int            `json:"occupied"`
	Vacant        int            `json:"vacant"`
	WaterUnits    moneyfmt.Money `json:"water_units"`
	WaterRate     moneyfmt.Money `json:"water_rate"`
	ExpectedWater moneyfmt.Money `json:"expected_water"`
	ActualWater   moneyfmt.Money `json:"actual_water"`
}

// Deviation is actual minus expected water billing.
func (s ScheduleSummary) Deviation() moneyfmt.Money { return s.ActualWater.Sub(s.ExpectedWater) }

// SchedulePayment is one dated payment inside a cell.
type SchedulePayment struct {
	Date   time.Time      `json:"date"`
	Amount moneyfmt.Money `json:"amount"`
}

// ScheduleRow is one unit. Multiple payments of one ledger type stack as
// separate date/amount lines within their cell.
type ScheduleRow struct {
	HouseNo            string            `json:"house_no"`
	Tenants            []string          `json:"tenants"` // tenant then co-payers, joined with "OR"
	Rent               []SchedulePayment `json:"rent"`
	Water              []SchedulePayment `json:"water"`
	Garbage            []SchedulePayment `json:"garbage"`
	RentDeposit        []SchedulePayment `json:"rent_deposit"`
	WaterDeposit       []SchedulePayment `json:"water_deposit"`
	ElectricityDeposit []SchedulePayment `json:"electricity_deposit"`
}

// ManagementFee is the closing "MANAGEMENT FEE: pct/100 X ..." line.
type ManagementFee struct {
	Percent string // e.g. "10"
	Fee     moneyfmt.Money
}

func sumPayments(ps []SchedulePayment) moneyfmt.Money {
	var t moneyfmt.Money
	for _, p := range ps {
		t = t.Add(p.Amount)
	}
	return t
}

// Total is everything the unit paid this period, across printed columns.
func (r ScheduleRow) Total(garbage, electricityDeposit bool) moneyfmt.Money {
	t := sumPayments(r.Rent).Add(sumPayments(r.Water)).Add(sumPayments(r.RentDeposit)).Add(sumPayments(r.WaterDeposit))
	if garbage {
		t = t.Add(sumPayments(r.Garbage))
	}
	if electricityDeposit {
		t = t.Add(sumPayments(r.ElectricityDeposit))
	}
	return t
}

// GrandTotal is the sum of every row's total.
func (s MonthlySchedule) GrandTotal() moneyfmt.Money {
	var t moneyfmt.Money
	for _, r := range s.Rows {
		t = t.Add(r.Total(s.GarbageEnabled, s.ElectricityDepositShown))
	}
	return t
}

// BuildMonthlySchedule renders the schedule, landscape A4.
func BuildMonthlySchedule(s MonthlySchedule) ([]byte, error) {
	m := newDocument(pagesize.A4, true, "Payments Schedule")
	m.AddRows(s.rows()...)
	return render(m)
}

type column struct {
	header string
	width  int // relative; normalised to the grid
}

func (s MonthlySchedule) columns() []column {
	cols := []column{
		{"HSE NO.'S", 6}, {"TENANTS NAMES", 16},
		{"DATE", 7}, {"RENT PAID (KSH.)", 10},
		{"DATE", 7}, {"WATER BILLS PAID (KSHS.)", 10},
	}
	if s.GarbageEnabled {
		cols = append(cols, column{"DATE", 6}, column{"GARBAGE PAID (KSHS.)", 8})
	}
	cols = append(cols,
		column{"DATE", 7}, column{"RENT DEPOSITS PAID (KSHS.)", 10},
		column{"DATE", 7}, column{"WATER DEPOSITS PAID (KSHS.)", 10},
	)
	if s.ElectricityDepositShown {
		cols = append(cols, column{"DATE", 6}, column{"ELECTRICITY DEPOSITS PAID (KSHS.)", 10})
	}
	cols = append(cols, column{"TOTAL AMOUNTS PAID (KSHS.)", 10})
	sum := 0
	for _, c := range cols {
		sum += c.width
	}
	acc := 0
	for i := range cols {
		if i == len(cols)-1 {
			cols[i].width = gridSize - acc
			break
		}
		cols[i].width = cols[i].width * gridSize / sum
		acc += cols[i].width
	}
	return cols
}

// boxCol boxes a cell on all four sides, applied per column rather than per
// row, so the grid gets inner vertical dividers too (a Rundas TableGrid
// look), not just the row's outer rectangle. Reuses water_invoice.go's
// `boxed` cell style.
func boxCol(w int, comps ...core.Component) core.Col {
	return col.New(w).Add(comps...).WithStyle(boxed)
}

func (s MonthlySchedule) rows() []core.Row {
	th := s.Theme
	var rows []core.Row

	// Header, then the table, then NOTE:1, NOTE:2 and the fee line — the
	// Rundas source's order; NOTE:1 does not sit above the table.
	rows = append(rows, textRow(9, strings.ToUpper(s.PropertyName), props.Text{Size: 16, Style: fontstyle.Bold, Align: align.Center, Color: th.accentColor()}))
	if s.Location != "" {
		rows = append(rows, textRow(5, strings.ToUpper(s.Location), props.Text{Size: 10, Align: align.Center}))
	}
	rows = append(rows, textRow(7, "ALL IN ONE PAYMENTS SCHEDULE: MONTH: "+MonthLabel(s.Period), props.Text{Size: 11, Style: fontstyle.Bold, Align: align.Center}))
	rows = append(rows, row.New(2).Add(col.New(gridSize).Add(line.New(props.Line{Color: th.accentColor(), Thickness: 0.6}))))
	rows = append(rows, spacer(2))

	cols := s.columns()
	header := row.New(12)
	for _, c := range cols {
		header.Add(boxCol(c.width, text.New(c.header, props.Text{Size: 6.5, Style: fontstyle.Bold, Align: align.Center, Top: 1})))
	}
	rows = append(rows, header)

	var t [6]moneyfmt.Money // rent, water, garbage, rent deposit, water deposit, electricity deposit
	for _, r := range s.Rows {
		rows = append(rows, s.tableRow(r, cols))
		t[0] = t[0].Add(sumPayments(r.Rent))
		t[1] = t[1].Add(sumPayments(r.Water))
		t[2] = t[2].Add(sumPayments(r.Garbage))
		t[3] = t[3].Add(sumPayments(r.RentDeposit))
		t[4] = t[4].Add(sumPayments(r.WaterDeposit))
		t[5] = t[5].Add(sumPayments(r.ElectricityDeposit))
	}
	rows = append(rows, s.totalsRow(cols, t))

	rows = append(rows, notesBlock("NOTE: 1", s.Note1)...)
	rows = append(rows, notesBlock("NOTE: 2", s.Notes)...)

	rows = append(rows, spacer(3))
	// The fee is a percentage of rent collected (t[0]), not the grand
	// total — matches the Rundas source's "5/100 X KSH. 567,000" line.
	// The date is the fee's posting date (the period's close), not
	// whatever wall-clock time the PDF happens to be rendered or
	// confirmed at.
	fee := fmt.Sprintf("MANAGEMENT FEE: %s/100 X KSH. %s = KSHS. %s     %s",
		s.ManagementFee.Percent, scheduleAmount(t[0]), scheduleAmount(s.ManagementFee.Fee), scheduleDate(periodEnd(s.Period)))
	rows = append(rows, textRow(5, fee, props.Text{Size: 10, Style: fontstyle.Bold, Align: align.Right}))
	return rows
}

// periodEnd is the last calendar day of the billing month — the
// management fee's posting date, stable and ledger-derived rather than
// whatever wall-clock time happens to confirm or render the PDF.
func periodEnd(period time.Time) time.Time {
	return period.AddDate(0, 1, 0).AddDate(0, 0, -1)
}

// notesBlock renders a frozen NOTE:1/NOTE:2 block verbatim: a bold header
// followed by each line as-is. Nothing here is recomputed — the lines
// already came out of the confirmation snapshot.
func notesBlock(header string, lines []string) []core.Row {
	if len(lines) == 0 {
		return nil
	}
	rows := make([]core.Row, 0, len(lines)+2)
	rows = append(rows, spacer(3), textRow(5, header, props.Text{Size: 9, Style: fontstyle.Bold}))
	for _, l := range lines {
		rows = append(rows, textRow(4.5, l, props.Text{Size: 9}))
	}
	return rows
}

// scheduleAmount trims a money value's ".00": Rundas amounts print as
// whole shillings ("14,000"), never with cents, for round figures.
func scheduleAmount(m moneyfmt.Money) string {
	return strings.TrimSuffix(m.Display(), ".00")
}

// scheduleDate is D/M/YYYY with no leading zeros, as the Rundas original
// writes it (e.g. "7/8/2026") — DisplayDate's zero-padded form is for the
// other paper documents (receipts, invoices), not this one.
func scheduleDate(t time.Time) string {
	return fmt.Sprintf("%d/%d/%d", t.Day(), t.Month(), t.Year())
}

// stack places one text per line at increasing offsets, so a cell with
// several payments shows each on its own line.
func stack(lines []string, p props.Text) []core.Component {
	out := make([]core.Component, 0, len(lines))
	for i, l := range lines {
		q := p
		q.Top = 1 + float64(i)*4
		out = append(out, text.New(l, q))
	}
	return out
}

// Characters that fit on one line of the house and tenant columns at 8pt. A
// value longer than that wraps, and the row must be as tall as the wrapped
// lines or the next row's text is drawn over it.
const (
	houseChars  = 7
	tenantChars = 20
)

// wrapWords breaks s on spaces into lines of at most max characters (a single
// longer word is kept whole).
func wrapWords(s string, max int) []string {
	if len(s) <= max {
		return []string{s}
	}
	var lines []string
	cur := ""
	for _, w := range strings.Fields(s) {
		switch {
		case cur == "":
			cur = w
		case len(cur)+1+len(w) <= max:
			cur += " " + w
		default:
			lines = append(lines, cur)
			cur = w
		}
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	return lines
}

func dates(ps []SchedulePayment) []string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = scheduleDate(p.Date)
	}
	return out
}

func amounts(ps []SchedulePayment) []string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = scheduleAmount(p.Amount)
	}
	return out
}

func (s MonthlySchedule) tableRow(r ScheduleRow, cols []column) core.Row {
	tenantLines := make([]string, 0, len(r.Tenants)*2)
	for i, t := range r.Tenants {
		if i > 0 {
			tenantLines = append(tenantLines, "OR")
		}
		tenantLines = append(tenantLines, wrapWords(t, tenantChars)...)
	}

	cells := [][]string{wrapWords(r.HouseNo, houseChars), tenantLines,
		dates(r.Rent), amounts(r.Rent),
		dates(r.Water), amounts(r.Water),
	}
	if s.GarbageEnabled {
		cells = append(cells, dates(r.Garbage), amounts(r.Garbage))
	}
	cells = append(cells,
		dates(r.RentDeposit), amounts(r.RentDeposit),
		dates(r.WaterDeposit), amounts(r.WaterDeposit),
	)
	if s.ElectricityDepositShown {
		cells = append(cells, dates(r.ElectricityDeposit), amounts(r.ElectricityDeposit))
	}
	if total := r.Total(s.GarbageEnabled, s.ElectricityDepositShown); !total.IsZero() {
		cells = append(cells, []string{scheduleAmount(total)})
	} else {
		cells = append(cells, nil)
	}

	maxLines := 1
	for _, c := range cells {
		maxLines = max(maxLines, len(c))
	}
	rw := row.New(float64(maxLines)*4 + 3)
	for i, c := range cells {
		al := align.Center
		if i == 1 {
			al = align.Left
		}
		rw.Add(boxCol(cols[i].width, stack(c, props.Text{Size: 8, Align: al})...))
	}
	return rw
}

// totalsRow is the grand-total row, each cell stacking its label (pre-broken
// at known word boundaries, so it never overlaps the value below it) and
// then the value, as the Rundas original does. The row's height grows to
// fit whichever cell wraps to the most lines.
func (s MonthlySchedule) totalsRow(cols []column, t [6]moneyfmt.Money) core.Row {
	type cell struct {
		label []string
		value string
	}
	cells := []cell{{}, {}, {}, {[]string{"RENT PAID."}, scheduleAmount(t[0])}, {}, {[]string{"WATER BILLS", "PAID."}, scheduleAmount(t[1])}}
	if s.GarbageEnabled {
		cells = append(cells, cell{}, cell{[]string{"GARBAGE PAID."}, scheduleAmount(t[2])})
	}
	cells = append(cells,
		cell{}, cell{[]string{"RENT DEPOSIT", "PAID."}, scheduleAmount(t[3])},
		cell{}, cell{[]string{"WATER DEPOSIT", "PAID."}, scheduleAmount(t[4])},
	)
	if s.ElectricityDepositShown {
		cells = append(cells, cell{}, cell{[]string{"ELECTRICITY DEPOSIT", "PAID."}, scheduleAmount(t[5])})
	}
	cells = append(cells, cell{[]string{"GRAND TOTAL", "PAYMENT."}, scheduleAmount(s.GrandTotal())})

	maxLines := 1
	for _, c := range cells {
		maxLines = max(maxLines, len(c.label)+1)
	}
	rw := row.New(float64(maxLines)*4 + 3)
	for i, c := range cells {
		lines := append(append([]string{}, c.label...), c.value)
		rw.Add(boxCol(cols[i].width, stack(lines, props.Text{Size: 8, Style: fontstyle.Bold, Align: align.Center})...))
	}
	return rw
}
