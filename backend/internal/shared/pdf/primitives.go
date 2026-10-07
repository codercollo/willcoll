// Package pdf holds the product-agnostic maroto rendering primitives used
// by every product's document builders: page setup, page/2-up layout,
// row helpers, and the date formatting shared across printed documents.
// It has no knowledge of Willcoll's domain — no Theme, no receipts, no
// invoices. Each product keeps its own document builders (e.g.
// internal/rentals/pdf) on top of this layer.
package pdf

import (
	"strings"
	"time"

	"github.com/johnfercher/maroto/v2"
	"github.com/johnfercher/maroto/v2/pkg/components/col"
	"github.com/johnfercher/maroto/v2/pkg/components/line"
	"github.com/johnfercher/maroto/v2/pkg/components/page"
	"github.com/johnfercher/maroto/v2/pkg/components/row"
	"github.com/johnfercher/maroto/v2/pkg/components/text"
	"github.com/johnfercher/maroto/v2/pkg/config"
	"github.com/johnfercher/maroto/v2/pkg/consts/fontstyle"
	"github.com/johnfercher/maroto/v2/pkg/consts/linestyle"
	"github.com/johnfercher/maroto/v2/pkg/consts/orientation"
	"github.com/johnfercher/maroto/v2/pkg/consts/pagesize"
	"github.com/johnfercher/maroto/v2/pkg/core"
	"github.com/johnfercher/maroto/v2/pkg/props"
)

// DisplayDate is how dates appear on every printed document: 23/09/2026,
// as written on the paper originals.
func DisplayDate(t time.Time) string {
	return t.Format("02/01/2006")
}

// MonthLabel renders a billing period as "SEPTEMBER 2026".
func MonthLabel(t time.Time) string {
	return strings.ToUpper(t.Format("January 2006"))
}

// Black and Grey are the two ink colours every plain black-on-white
// printed document uses, like the paper originals.
var (
	Black = &props.Color{Red: 0, Green: 0, Blue: 0}
	Grey  = &props.Color{Red: 110, Green: 110, Blue: 110}
)

// GridSize is columns sized as percentages of the page width.
const GridSize = 100

// NewDocument returns a maroto document with the shared settings.
// Compression is off so documents stay greppable in tests; they're small.
func NewDocument(size pagesize.Type, landscape bool, title string) core.Maroto {
	b := config.NewBuilder().
		WithPageSize(size).
		WithMaxGridSize(GridSize).
		WithLeftMargin(10).
		WithRightMargin(10).
		WithTopMargin(8).
		WithBottomMargin(6).
		WithCompression(false).
		WithTitle(title, true).
		WithCreator("Willcoll", true).
		// Times, not maroto's default Arial/Helvetica: the paper originals
		// (Rundas, the KIWI PLACE book) are all typed in a serif face.
		WithDefaultFont(&props.Font{Family: "times"})
	if landscape {
		b = b.WithOrientation(orientation.Horizontal)
	}
	return maroto.New(b.Build())
}

// AddPages puts each document's rows on a page of its own. A document that
// runs longer than a page is a layout bug (the tests check page counts), not
// something to paper over with page-break rows, which left blank pages.
func AddPages(m core.Maroto, docs ...[]core.Row) {
	pages := make([]core.Page, 0, len(docs))
	for _, rows := range docs {
		pages = append(pages, page.New().Add(rows...))
	}
	m.AddPages(pages...)
}

// Build2Up lays two documents per A4 portrait page, separated by a dashed
// cut line — the "2 bills per sheet" layout the paper originals use. An
// odd document out gets a page to itself, cut line included, so a manager
// can still cut every sheet the same way.
func Build2Up(title string, docs [][]core.Row) ([]byte, error) {
	m := NewDocument(pagesize.A4, false, title)
	cutLine := row.New(4).Add(col.New(GridSize).Add(line.New(props.Line{Color: Grey, Thickness: 0.3, Style: linestyle.Dashed})))

	var pages []core.Page
	for i := 0; i < len(docs); i += 2 {
		rows := append([]core.Row{}, docs[i]...)
		rows = append(rows, cutLine)
		if i+1 < len(docs) {
			rows = append(rows, docs[i+1]...)
		}
		pages = append(pages, page.New().Add(rows...))
	}
	m.AddPages(pages...)
	return Render(m)
}

// Render generates the document's bytes.
func Render(m core.Maroto) ([]byte, error) {
	doc, err := m.Generate()
	if err != nil {
		return nil, err
	}
	return doc.GetBytes(), nil
}

// TextRow is a full-width row holding one text.
func TextRow(height float64, s string, p props.Text) core.Row {
	return row.New(height).Add(col.New(GridSize).Add(text.New(s, p)))
}

// LabelValueRow is "Label ........ value" with the value underlined by a
// rule, like a filled-in paper form.
func LabelValueRow(label, value string, labelWidth int, bold bool) core.Row {
	valueStyle := fontstyle.Normal
	if bold {
		valueStyle = fontstyle.Bold
	}
	return row.New(7).Add(
		col.New(labelWidth).Add(text.New(label, props.Text{Size: 10, Top: 1.5})),
		col.New(GridSize-labelWidth).Add(
			text.New(value, props.Text{Size: 10, Top: 1.5, Left: 1, Style: valueStyle}),
			line.New(props.Line{Color: Grey, Thickness: 0.2, OffsetPercent: 95}),
		),
	)
}

// Spacer is an empty row.
func Spacer(height float64) core.Row {
	return row.New(height)
}
