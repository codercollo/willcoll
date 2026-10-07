package pdf

import (
	"time"

	"github.com/codercollo/willcoll/backend/pkg/moneyfmt"
	"github.com/johnfercher/maroto/v2/pkg/consts/align"
	"github.com/johnfercher/maroto/v2/pkg/consts/fontstyle"
	"github.com/johnfercher/maroto/v2/pkg/consts/pagesize"
	"github.com/johnfercher/maroto/v2/pkg/props"
)

// SubscriptionInvoice is Willcoll's own invoice to a manager for a platform
// subscription (build-phases.txt 3c-iv-b1: shared because platform-api and
// every product API's billing handlers all render one). It is never
// tenant-facing and uses Willcoll's branding, not a property Theme.
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
	m := NewDocument(pagesize.A4, false, "Willcoll Invoice")
	m.AddRows(
		TextRow(12, "WILLCOLL", props.Text{Size: 22, Style: fontstyle.Bold, Align: align.Left}),
		TextRow(5, "Property & rent reconciliation", props.Text{Size: 9, Color: Grey}),
		Spacer(6),
		TextRow(8, "SUBSCRIPTION INVOICE", props.Text{Size: 14, Style: fontstyle.Bold}),
		LabelValueRow("Invoice No.", inv.InvoiceNo, 25, false),
		LabelValueRow("Date", DisplayDate(inv.IssuedAt), 25, false),
		LabelValueRow("Billed to", inv.ManagerName, 25, false),
		LabelValueRow("Email", inv.ManagerEmail, 25, false),
		LabelValueRow("Period", MonthLabel(inv.Period), 25, false),
		Spacer(8),
		LabelValueRow("Description", desc, 25, false),
		LabelValueRow("Amount (Kshs)", inv.Amount.Display(), 25, false),
		Spacer(4),
		LabelValueRow("TOTAL DUE (Kshs)", inv.Amount.Display(), 25, true),
	)
	return Render(m)
}
