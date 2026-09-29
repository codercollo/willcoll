package pdf

import (
	"time"

	"github.com/codercollo/willcoll/backend/pkg/moneyfmt"
	"github.com/johnfercher/maroto/v2/pkg/consts/align"
	"github.com/johnfercher/maroto/v2/pkg/consts/fontstyle"
	"github.com/johnfercher/maroto/v2/pkg/consts/pagesize"
	"github.com/johnfercher/maroto/v2/pkg/props"
)

// SubscriptionInvoice is Willcoll's own invoice to a manager for the
// platform subscription. It is never tenant-facing and uses Willcoll's
// branding, not a property Theme.
type SubscriptionInvoice struct {
	InvoiceNo    string
	ManagerName  string
	ManagerEmail string
	Period       time.Time
	IssuedAt     time.Time
	Description  string // defaults to "Willcoll platform subscription"
	Amount       moneyfmt.Money
}

// BuildSubscriptionInvoice renders the invoice, portrait A4.
func BuildSubscriptionInvoice(inv SubscriptionInvoice) ([]byte, error) {
	desc := inv.Description
	if desc == "" {
		desc = "Willcoll platform subscription"
	}
	m := newDocument(pagesize.A4, false, "Willcoll Invoice")
	m.AddRows(
		textRow(12, "WILLCOLL", props.Text{Size: 22, Style: fontstyle.Bold, Align: align.Left}),
		textRow(5, "Property & rent reconciliation", props.Text{Size: 9, Color: grey}),
		spacer(6),
		textRow(8, "SUBSCRIPTION INVOICE", props.Text{Size: 14, Style: fontstyle.Bold}),
		labelValueRow("Invoice No.", inv.InvoiceNo, 25, false),
		labelValueRow("Date", DisplayDate(inv.IssuedAt), 25, false),
		labelValueRow("Billed to", inv.ManagerName, 25, false),
		labelValueRow("Email", inv.ManagerEmail, 25, false),
		labelValueRow("Period", MonthLabel(inv.Period), 25, false),
		spacer(8),
		labelValueRow("Description", desc, 25, false),
		labelValueRow("Amount (Kshs)", inv.Amount.Display(), 25, false),
		spacer(4),
		labelValueRow("TOTAL DUE (Kshs)", inv.Amount.Display(), 25, true),
	)
	return render(m)
}
