package pdf

import (
	"fmt"
	"strconv"
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

// Ledger types a receipt can carry (matching the ledger's own names).
const (
	LedgerRent               = "RENT"
	LedgerWater              = "WATER"
	LedgerGarbage            = "GARBAGE"
	LedgerRentDeposit        = "RENT_DEPOSIT"
	LedgerWaterDeposit       = "WATER_DEPOSIT"
	LedgerElectricityDeposit = "ELECTRICITY_DEPOSIT"
)

// Payment methods for the receipt's "Cash / Mpesa / Cheque" line. Cheque is
// never selected: the ledger has no field distinguishing it from cash, so
// a "manual" payment always renders as Cash.
const (
	MethodCash   = "cash"
	MethodMpesa  = "mpesa"
	MethodCheque = "cheque"
)

// Receipt is one KIWI PLACE-style carbon-book receipt for exactly one
// payment allocation — one ledger type, one amount, one receipt number.
// A single M-Pesa payment split across rent and water produces two
// Receipts, each with its own number, both carrying the same MpesaCode.
type Receipt struct {
	Theme        Theme
	PropertyName string
	ReceiptNo    int64 // receipts.receipt_no, sequential per property, printed in red
	Date         time.Time
	Period       time.Time // the month this payment was received in
	// SettledPeriod is the month this payment's RENT allocation actually
	// paid off, when that's an older month than Period (carried-over
	// arrears): the rent line then names SettledPeriod, marked "(ARREARS)",
	// instead of Period. Nil for the ordinary case — the payment settles
	// its own received month.
	SettledPeriod *time.Time

	// ReceivedFrom is the lease's tenant_name followed by any co-payers
	// (lease_payers); they print joined with "OR".
	ReceivedFrom []string
	HouseNo      string

	LedgerType string
	Amount     moneyfmt.Money
	// ArrearsNote is filled only when this allocation cleared money owed
	// from before this period, e.g. "ARREARS CLEARED: KSH. 12,000".
	ArrearsNote string

	Method    string // MethodCash, MethodMpesa or MethodCheque
	MpesaCode string // shown only when Method is MethodMpesa

	// Void marks a receipt whose allocation was later reversed. The
	// number and every other field stay exactly as issued; only a VOID
	// stamp and the reason are added — the paper carbon book is never
	// torn out or renumbered.
	Void       bool
	VoidReason string
}

func ledgerLabel(ledgerType string) string {
	switch ledgerType {
	case LedgerWater:
		return "WATER"
	case LedgerGarbage:
		return "GARBAGE"
	case LedgerWaterDeposit:
		return "WATER DEPOSIT"
	case LedgerRentDeposit:
		return "RENT DEPOSIT"
	case LedgerElectricityDeposit:
		return "ELECTRICITY DEPOSIT"
	default:
		return "RENT"
	}
}

// receiptDate is D/M/YY, as the KIWI PLACE book writes it.
func receiptDate(t time.Time) string {
	return fmt.Sprintf("%d/%d/%02d", t.Day(), t.Month(), t.Year()%100)
}

// BuildReceipt renders one receipt.
func BuildReceipt(r Receipt) ([]byte, error) {
	return BuildReceipts([]Receipt{r})
}

// BuildReceipts renders several receipts into one PDF, one per page.
func BuildReceipts(receipts []Receipt) ([]byte, error) {
	m := newDocument(pagesize.A5, true, "Receipts")
	docs := make([][]core.Row, 0, len(receipts))
	for _, r := range receipts {
		docs = append(docs, r.rows())
	}
	addPages(m, docs...)
	return render(m)
}

var receiptRed = &props.Color{Red: 180, Green: 0, Blue: 0}

// rentLine is the "Being payment of:" bullet's label and whether it's
// naming a carried-over arrears month rather than the period the payment
// was received in.
func (r Receipt) rentLine() (label string, arrears bool) {
	period := r.Period
	if r.SettledPeriod != nil {
		period, arrears = *r.SettledPeriod, true
	}
	label = MonthLabel(period) + " " + ledgerLabel(r.LedgerType)
	if arrears {
		label += " (ARREARS)"
	}
	return label + ":", arrears
}

func (r Receipt) rows() []core.Row {
	rows := []core.Row{
		row.New(8).Add(
			col.New(60).Add(text.New("Date: "+receiptDate(r.Date), props.Text{Size: 9, Top: 2})),
			col.New(40).Add(
				text.New("Original Receipt No. "+strconv.FormatInt(r.ReceiptNo, 10), props.Text{Size: 10, Style: fontstyle.Bold, Align: align.Center, Top: 3, Color: receiptRed}),
			).WithStyle(boxed),
		),
		spacer(1),
	}
	if r.Void {
		rows = append(rows, textRow(6, "VOID"+voidSuffix(r.VoidReason), props.Text{Size: 12, Style: fontstyle.Bold, Align: align.Center, Color: receiptRed}))
	}

	rentLabel, _ := r.rentLine()

	rows = append(rows,
		textRow(7, strings.ToUpper(r.PropertyName), props.Text{Size: 14, Style: fontstyle.Bold, Align: align.Center, Color: r.Theme.accentColor()}),
		textRow(4.5, addressLine(r.Theme), props.Text{Size: 8, Align: align.Center}),
		spacer(2),
		labelValueRow("Received from", strings.Join(r.ReceivedFrom, " OR "), 22, true),
		labelValueRow("The sum shillings", strings.ToUpper(r.Amount.Words())+".", 22, false),
		spacer(1),
		textRow(6, "Being payment of:", props.Text{Size: 10, Top: 1}),
		bulletRow(rentLabel, "", receiptAmount(r.Amount), false),
	)
	if r.ArrearsNote != "" {
		rows = append(rows, bulletRow("Arrears", r.ArrearsNote, "", false))
	}
	rows = append(rows, bulletRow("House No.", r.HouseNo, "", false))

	rows = append(rows,
		spacer(2),
		row.New(10).Add(
			col.New(12).Add(text.New("Kshs:", props.Text{Size: 11, Style: fontstyle.Bold, Top: 2.5})),
			col.New(28).Add(text.New(receiptAmount(r.Amount), props.Text{Size: 12, Style: fontstyle.Bold, Top: 2.5, Align: align.Center})).WithStyle(boxed),
			col.New(60).Add(methodComponents(r.Method)...),
		),
	)
	if r.Method == MethodMpesa && r.MpesaCode != "" {
		rows = append(rows, textRow(5, "M-Pesa Code: "+r.MpesaCode, props.Text{Size: 9, Top: 1}))
	}

	rows = append(rows,
		spacer(3),
		row.New(7).Add(
			col.New(55).Add(text.New("With Thanks, For: "+r.PropertyName, props.Text{Size: 10, Style: fontstyle.Bold, Top: 3})),
			col.New(45).Add(line.New(props.Line{Color: black, Thickness: 0.3, OffsetPercent: 90})),
		),
		row.New(4).Add(col.New(55), col.New(45).Add(text.New("Signature", props.Text{Size: 8, Align: align.Center, Color: grey}))),
	)
	return rows
}

// receiptAmount is the "9,000/=" whole-shillings form every receipt figure
// prints in, no cents — the same trimming as scheduleAmount.
func receiptAmount(m moneyfmt.Money) string {
	return scheduleAmount(m) + "/="
}

func voidSuffix(reason string) string {
	if reason == "" {
		return ""
	}
	return " - " + strings.ToUpper(reason)
}

func addressLine(th Theme) string {
	var parts []string
	parts = append(parts, th.AddressLines...)
	if th.Phone != "" {
		parts = append(parts, "TEL: "+th.Phone)
	}
	return strings.Join(parts, "   ")
}

// bulletRow is one "- label   detail ....... amount" line. The row is
// taller than a single text line needs: a long label (e.g. "SEPTEMBER 2026
// ELECTRICITY DEPOSIT:") wraps inside its narrow column, and a fixed
// single-line height let the wrapped second line spill into the row below,
// overlapping it (2026-09-28 regression — garbled text where the rent line
// and "House No." line overlapped).
func bulletRow(label, detail, amount string, struck bool) core.Row {
	style := fontstyle.Normal
	if struck {
		style = fontstyle.Strikethrough
	}
	return row.New(9).Add(
		col.New(4).Add(text.New("-", props.Text{Size: 10, Top: 1, Align: align.Center})),
		col.New(26).Add(text.New(label, props.Text{Size: 9, Top: 1, Style: style})),
		col.New(40).Add(
			text.New(detail, props.Text{Size: 9, Top: 1.3}),
			line.New(props.Line{Color: grey, Thickness: 0.2, OffsetPercent: 95}),
		),
		col.New(30).Add(
			text.New(amount, props.Text{Size: 10, Top: 1, Align: align.Right, Right: 2, Style: fontstyle.Bold}),
			line.New(props.Line{Color: grey, Thickness: 0.2, OffsetPercent: 95}),
		),
	)
}

// methodComponents lays out "Cash   Mpesa   Cheque" with the two methods
// NOT used struck through — never the method actually used.
func methodComponents(method string) []core.Component {
	entries := []struct{ id, label string }{
		{MethodCash, "Cash"}, {MethodMpesa, "Mpesa"}, {MethodCheque, "Cheque"},
	}
	comps := make([]core.Component, 0, len(entries))
	left := 0.0
	for _, e := range entries {
		style := fontstyle.Strikethrough
		if e.id == method {
			style = fontstyle.Normal
		}
		comps = append(comps, text.New(e.label, props.Text{Size: 10, Top: 3, Left: left, Style: style}))
		left += float64(len(e.label))*2 + 8
	}
	return comps
}
