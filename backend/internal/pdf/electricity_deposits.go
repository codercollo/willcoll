package pdf

import (
	"strings"

	"github.com/codercollo/willcoll/backend/pkg/moneyfmt"
	"github.com/johnfercher/maroto/v2/pkg/components/col"
	"github.com/johnfercher/maroto/v2/pkg/components/row"
	"github.com/johnfercher/maroto/v2/pkg/components/text"
	"github.com/johnfercher/maroto/v2/pkg/consts/align"
	"github.com/johnfercher/maroto/v2/pkg/consts/fontstyle"
	"github.com/johnfercher/maroto/v2/pkg/consts/pagesize"
	"github.com/johnfercher/maroto/v2/pkg/core"
	"github.com/johnfercher/maroto/v2/pkg/props"
)

// ElectricityDepositRow is one unit's line on the property tab's export.
type ElectricityDepositRow struct {
	HouseNo    string
	TenantName string
	Required   moneyfmt.Money
	Paid       moneyfmt.Money
	Balance    moneyfmt.Money
}

// electricityDepositCols is the export table's column widths, out of gridSize.
var electricityDepositCols = []int{18, 34, 16, 16, 16}

// BuildElectricityDeposits renders the property's electricity deposit
// spreadsheet: HOUSE NO | TENANT | DEPOSIT REQUIRED | PAID | BALANCE, with a
// totals row.
func BuildElectricityDeposits(propertyName string, rows []ElectricityDepositRow, totalRequired, totalPaid, totalBalance moneyfmt.Money) ([]byte, error) {
	m := newDocument(pagesize.A4, false, "Electricity Deposits")

	headers := []string{"House No.", "Tenant", "Deposit Required", "Paid", "Balance"}
	aligns := []align.Type{align.Left, align.Left, align.Right, align.Right, align.Right}

	body := []core.Row{
		textRow(8, strings.ToUpper(propertyName), props.Text{Size: 14, Style: fontstyle.Bold, Align: align.Center}),
		textRow(6, "ELECTRICITY DEPOSITS", props.Text{Size: 11, Style: fontstyle.Bold, Align: align.Center}),
		spacer(3),
	}

	headerCols := make([]core.Col, len(headers))
	for i, h := range headers {
		headerCols[i] = col.New(electricityDepositCols[i]).Add(text.New(h, props.Text{Size: 9, Style: fontstyle.Bold, Align: aligns[i]}))
	}
	body = append(body, row.New(6).Add(headerCols...))

	for _, r := range rows {
		cells := []string{r.HouseNo, r.TenantName, r.Required.Display(), r.Paid.Display(), r.Balance.Display()}
		rowCols := make([]core.Col, len(cells))
		for i, c := range cells {
			rowCols[i] = col.New(electricityDepositCols[i]).Add(text.New(c, props.Text{Size: 9, Align: aligns[i]}))
		}
		body = append(body, row.New(5).Add(rowCols...))
	}

	body = append(body,
		spacer(2),
		row.New(6).Add(
			col.New(electricityDepositCols[0]+electricityDepositCols[1]).Add(text.New("TOTALS", props.Text{Size: 9, Style: fontstyle.Bold})),
			col.New(electricityDepositCols[2]).Add(text.New(totalRequired.Display(), props.Text{Size: 9, Style: fontstyle.Bold, Align: align.Right})),
			col.New(electricityDepositCols[3]).Add(text.New(totalPaid.Display(), props.Text{Size: 9, Style: fontstyle.Bold, Align: align.Right})),
			col.New(electricityDepositCols[4]).Add(text.New(totalBalance.Display(), props.Text{Size: 9, Style: fontstyle.Bold, Align: align.Right})),
		),
	)

	addPages(m, body)
	return render(m)
}
