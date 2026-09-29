package pdf

import (
	"time"

	"github.com/codercollo/willcoll/backend/pkg/moneyfmt"
	"github.com/johnfercher/maroto/v2/pkg/consts/pagesize"
	"github.com/johnfercher/maroto/v2/pkg/core"
)

// GarbageInvoice is one unit's garbage collection bill for one period: the
// garbage variant of the "WATER AND GARBAGE COLLECTION BILL" slip. Same
// template as the water bill, with the meter section replaced by the
// property's fixed fee. Only rendered for properties with garbage billing
// enabled.
type GarbageInvoice struct {
	Theme        Theme
	CustomerName string
	HouseNo      string
	Period       time.Time
	BillDate     time.Time

	Fee moneyfmt.Money // garbage_runs.fee_snapshot

	// PriorBalance is the unit's GARBAGE balance before this run's charge.
	PriorBalance moneyfmt.Money

	Payment PaymentParticulars
}

// TotalDue is the prior balance plus this period's fee.
func (g GarbageInvoice) TotalDue() moneyfmt.Money { return g.PriorBalance.Add(g.Fee) }

// BuildGarbageInvoice renders one unit's garbage bill.
func BuildGarbageInvoice(inv GarbageInvoice) ([]byte, error) {
	return BuildGarbageInvoices([]GarbageInvoice{inv})
}

// BuildGarbageInvoices renders a property's garbage run as one PDF, one
// bill per A5 page — the single-bill option.
func BuildGarbageInvoices(invoices []GarbageInvoice) ([]byte, error) {
	m := newDocument(pagesize.A5, false, "Garbage collection bills")
	addPages(m, garbageInvoiceDocs(invoices)...)
	return render(m)
}

// BuildGarbageInvoices2Up renders the same bills two to an A4 portrait
// sheet with a cut line between them, like the paper originals.
func BuildGarbageInvoices2Up(invoices []GarbageInvoice) ([]byte, error) {
	return build2Up("Garbage collection bills", garbageInvoiceDocs(invoices))
}

func garbageInvoiceDocs(invoices []GarbageInvoice) [][]core.Row {
	docs := make([][]core.Row, 0, len(invoices))
	for _, inv := range invoices {
		s := slip{
			theme:        inv.Theme,
			title:        "GARBAGE COLLECTION BILL",
			period:       inv.Period,
			customerName: inv.CustomerName,
			houseNo:      inv.HouseNo,
			dateLabel:    "Date",
			date:         inv.BillDate,
			lines: []slipLine{
				{label: "Garbage Collection Fee (Kshs)", value: inv.Fee.Display(), bold: true},
			},
			priorBalance: inv.PriorBalance,
			totalDue:     inv.TotalDue(),
			payment:      inv.Payment,
		}
		docs = append(docs, s.rows())
	}
	return docs
}
