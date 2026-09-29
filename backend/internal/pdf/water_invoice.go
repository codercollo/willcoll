package pdf

import (
	"time"

	"github.com/codercollo/willcoll/backend/pkg/moneyfmt"
	"github.com/johnfercher/maroto/v2/pkg/components/col"
	"github.com/johnfercher/maroto/v2/pkg/components/row"
	"github.com/johnfercher/maroto/v2/pkg/components/text"
	"github.com/johnfercher/maroto/v2/pkg/consts/align"
	"github.com/johnfercher/maroto/v2/pkg/consts/border"
	"github.com/johnfercher/maroto/v2/pkg/consts/fontstyle"
	"github.com/johnfercher/maroto/v2/pkg/consts/pagesize"
	"github.com/johnfercher/maroto/v2/pkg/core"
	"github.com/johnfercher/maroto/v2/pkg/props"
)

// WaterInvoice is one unit's water bill for one billing period: the water
// variant of the "WATER AND GARBAGE COLLECTION BILL" slip. It carries the
// meter fields and nothing about garbage.
type WaterInvoice struct {
	Theme        Theme
	CustomerName string // the active lease's tenant_name
	HouseNo      string // units.unit_code
	Period       time.Time
	ReadingDate  time.Time

	CurrentReading  moneyfmt.Money // numeric(12,2) meter values
	PreviousReading moneyfmt.Money
	UnitsConsumed   moneyfmt.Money
	Rate            moneyfmt.Money // water_readings.rate_snapshot, per m³
	Amount          moneyfmt.Money // this period's charge

	// PriorBalance is the unit's WATER balance before this run's charge
	// (system-design.txt 3.9 step 3: the arrears carried forward).
	PriorBalance moneyfmt.Money

	Payment PaymentParticulars
}

// TotalDue is the prior balance plus this period's charge.
func (w WaterInvoice) TotalDue() moneyfmt.Money { return w.PriorBalance.Add(w.Amount) }

// BuildWaterInvoice renders one unit's water bill.
func BuildWaterInvoice(inv WaterInvoice) ([]byte, error) {
	return BuildWaterInvoices([]WaterInvoice{inv})
}

// BuildWaterInvoices renders a property's whole water run as one PDF, one
// bill per A5 page, in the order given — the single-bill option.
func BuildWaterInvoices(invoices []WaterInvoice) ([]byte, error) {
	m := newDocument(pagesize.A5, false, "Water bills")
	addPages(m, waterInvoiceDocs(invoices)...)
	return render(m)
}

// BuildWaterInvoices2Up renders the same bills two to an A4 portrait sheet
// with a cut line between them, like the paper originals.
func BuildWaterInvoices2Up(invoices []WaterInvoice) ([]byte, error) {
	return build2Up("Water bills", waterInvoiceDocs(invoices))
}

func waterInvoiceDocs(invoices []WaterInvoice) [][]core.Row {
	docs := make([][]core.Row, 0, len(invoices))
	for _, inv := range invoices {
		s := slip{
			theme:        inv.Theme,
			title:        "WATER BILL",
			period:       inv.Period,
			customerName: inv.CustomerName,
			houseNo:      inv.HouseNo,
			dateLabel:    "Date of reading",
			date:         inv.ReadingDate,
			lines: []slipLine{
				{label: "Current Meter Reading", value: inv.CurrentReading.Display()},
				{label: "Previous Meter Reading", value: inv.PreviousReading.Display()},
				{label: "Consumption in Cubic Metres", value: inv.UnitsConsumed.Display()},
				{label: "Rate per Cubic Metre (Kshs)", value: inv.Rate.Display()},
				{label: "Cost this month (Kshs)", value: inv.Amount.Display(), bold: true},
			},
			priorBalance: inv.PriorBalance,
			totalDue:     inv.TotalDue(),
			payment:      inv.Payment,
		}
		docs = append(docs, s.rows())
	}
	return docs
}

// slip is the shared "WATER AND GARBAGE COLLECTION BILL" form. The water
// and garbage variants differ only in their charge lines.
type slip struct {
	theme        Theme
	title        string
	period       time.Time
	customerName string
	houseNo      string
	dateLabel    string
	date         time.Time
	lines        []slipLine
	priorBalance moneyfmt.Money
	totalDue     moneyfmt.Money
	payment      PaymentParticulars
}

type slipLine struct {
	label, value string
	bold         bool
}

var boxed = &props.Cell{BorderType: border.Full, BorderColor: black, BorderThickness: 0.3}

func (s slip) rows() []core.Row {
	var rows []core.Row
	rows = append(rows, letterhead(s.theme, 16)...)
	rows = append(rows,
		textRow(8, s.title, props.Text{Size: 13, Style: fontstyle.Bold, Align: align.Center, Top: 1.5}),
		textRow(6, "MONTH: "+MonthLabel(s.period), props.Text{Size: 10, Style: fontstyle.Bold, Align: align.Center}),
		spacer(3),
		labelValueRow("Customer's name:", s.customerName, 32, true),
		labelValueRow("House No.:", s.houseNo, 32, true),
		labelValueRow(s.dateLabel+":", DisplayDate(s.date), 32, false),
		spacer(4),
	)

	for _, l := range s.lines {
		rows = append(rows, figureRow(l.label, l.value, l.bold))
	}
	rows = append(rows,
		figureRow("Previous Total Balance B/F (Kshs)", s.priorBalance.Display(), false),
		row.New(9).Add(
			col.New(65).Add(text.New("TOTAL AMOUNT DUE TO DATE (Kshs)", props.Text{Size: 11, Style: fontstyle.Bold, Top: 2, Left: 1})).WithStyle(boxed),
			col.New(35).Add(text.New(s.totalDue.Display(), props.Text{Size: 11, Style: fontstyle.Bold, Top: 2, Right: 2, Align: align.Right})).WithStyle(boxed),
		),
		spacer(5),
		textRow(5, "PAYMENT DETAILS", props.Text{Size: 10, Style: fontstyle.Bold}),
		labelValueRow("Account Name:", s.payment.AccountName, 32, false),
		labelValueRow("Paybill / Account No.:", s.payment.AccountNumber, 32, false),
		labelValueRow("Bank:", s.payment.BankName, 32, false),
		spacer(4),
		textRow(10, reconnectionNote(s.theme), props.Text{Size: 8, Style: fontstyle.Italic}),
	)
	return rows
}

// figureRow is one "label | amount" line of the bill, boxed like the
// slip's printed grid.
func figureRow(label, value string, bold bool) core.Row {
	style := fontstyle.Normal
	if bold {
		style = fontstyle.Bold
	}
	return row.New(7).Add(
		col.New(65).Add(text.New(label, props.Text{Size: 10, Top: 1.5, Left: 1, Style: style})).WithStyle(boxed),
		col.New(35).Add(text.New(value, props.Text{Size: 10, Top: 1.5, Right: 2, Align: align.Right, Style: style})).WithStyle(boxed),
	)
}

func reconnectionNote(th Theme) string {
	if th.ReconnectionNote != "" {
		return th.ReconnectionNote
	}
	return DefaultReconnectionNote
}
