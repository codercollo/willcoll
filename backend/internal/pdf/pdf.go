// Package pdf renders Willcoll's printed documents with maroto
// (system-design.txt 4.10). Each builder reproduces a paper document the
// managing firm already uses:
//
//	receipt.go              the KIWI PLACE carbon receipt book
//	water_invoice.go        the "WATER AND GARBAGE COLLECTION BILL" slip, water variant
//	garbage_invoice.go      the same slip, garbage variant (no meter fields)
//	monthly_schedule.go     the "ALL IN ONE PAYMENTS SCHEDULE" landlord report
//	subscription_invoice.go Willcoll's own invoice to a manager (platform billing)
//
// It is a pure rendering layer: builders take already-loaded structs and
// return PDF bytes. No database, storage or HTTP calls happen here, so
// every builder is testable with fixture structs.
//
// Tenant-facing documents (the first four) are styled per property by a
// Theme: plain black ink on white, like the paper originals, with an
// optional accent colour used only for the header rule and title. The web
// app's navy/blue design tokens are never used on print.
package pdf

import (
	"fmt"
	"strings"
	"time"

	"github.com/johnfercher/maroto/v2"
	"github.com/johnfercher/maroto/v2/pkg/components/col"
	"github.com/johnfercher/maroto/v2/pkg/components/image"
	"github.com/johnfercher/maroto/v2/pkg/components/line"
	"github.com/johnfercher/maroto/v2/pkg/components/page"
	"github.com/johnfercher/maroto/v2/pkg/components/row"
	"github.com/johnfercher/maroto/v2/pkg/components/text"
	"github.com/johnfercher/maroto/v2/pkg/config"
	"github.com/johnfercher/maroto/v2/pkg/consts/align"
	"github.com/johnfercher/maroto/v2/pkg/consts/extension"
	"github.com/johnfercher/maroto/v2/pkg/consts/fontstyle"
	"github.com/johnfercher/maroto/v2/pkg/consts/linestyle"
	"github.com/johnfercher/maroto/v2/pkg/consts/orientation"
	"github.com/johnfercher/maroto/v2/pkg/consts/pagesize"
	"github.com/johnfercher/maroto/v2/pkg/core"
	"github.com/johnfercher/maroto/v2/pkg/props"
)

// Theme is a property's print styling, resolved by the service layer from
// properties.print_theme with fallbacks to the property's and landlord's
// own fields, so it is always complete enough to render.
type Theme struct {
	// HeaderText is the document's title line, e.g. "KIWI PLACE".
	HeaderText string
	// AddressLines follow the title, e.g. "P.O. Box 123-00100, Nairobi".
	AddressLines []string
	// Phone, printed as "Tel: ...". Optional.
	Phone string
	// Accent colours the header rule and title only; nil means black.
	Accent *RGB
	// Logo is an optional PNG or JPEG printed beside the header.
	Logo    []byte
	LogoExt string // "png" or "jpg"
	// ReconnectionNote is printed at the foot of water and garbage bills.
	// Empty means DefaultReconnectionNote.
	ReconnectionNote string
}

// RGB is a colour, e.g. from a "#1F4E79" accent.
type RGB struct{ R, G, B int }

// ParseHexColor parses "#RRGGBB".
func ParseHexColor(s string) (*RGB, error) {
	var c RGB
	if len(s) != 7 || s[0] != '#' {
		return nil, fmt.Errorf("pdf: %q is not a #RRGGBB colour", s)
	}
	if _, err := fmt.Sscanf(s[1:], "%02x%02x%02x", &c.R, &c.G, &c.B); err != nil {
		return nil, fmt.Errorf("pdf: %q is not a #RRGGBB colour", s)
	}
	return &c, nil
}

// DefaultReconnectionNote is printed on bills when a property doesn't set
// its own wording.
const DefaultReconnectionNote = "NB: Pay by the 10th of the month to avoid disconnection. A reconnection fee is charged on all disconnected accounts."

// PaymentParticulars is where tenants pay (the landlord's bank paybill).
type PaymentParticulars struct {
	AccountName   string
	AccountNumber string
	BankName      string
}

// DisplayDate is how dates appear on every printed document: 23/09/2026,
// as written on the paper originals.
func DisplayDate(t time.Time) string {
	return t.Format("02/01/2006")
}

// MonthLabel renders a billing period as "SEPTEMBER 2026".
func MonthLabel(t time.Time) string {
	return strings.ToUpper(t.Format("January 2006"))
}

// Colours and sizes shared by the tenant-facing documents: black ink on
// white, like the originals.
var (
	black = &props.Color{Red: 0, Green: 0, Blue: 0}
	grey  = &props.Color{Red: 110, Green: 110, Blue: 110}
)

const gridSize = 100 // columns sized as percentages of the page width

// newDocument returns a maroto document with the shared settings.
// Compression is off so documents stay greppable in tests; they're small.
func newDocument(size pagesize.Type, landscape bool, title string) core.Maroto {
	b := config.NewBuilder().
		WithPageSize(size).
		WithMaxGridSize(gridSize).
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

// render generates the document's bytes.
// addPages puts each document's rows on a page of its own. A document that
// runs longer than a page is a layout bug (the tests check page counts), not
// something to paper over with page-break rows, which left blank pages.
func addPages(m core.Maroto, docs ...[]core.Row) {
	pages := make([]core.Page, 0, len(docs))
	for _, rows := range docs {
		pages = append(pages, page.New().Add(rows...))
	}
	m.AddPages(pages...)
}

// build2Up lays two documents per A4 portrait page, separated by a dashed
// cut line — the "2 bills per sheet" layout the paper originals use. An
// odd document out gets a page to itself, cut line included, so a manager
// can still cut every sheet the same way.
func build2Up(title string, docs [][]core.Row) ([]byte, error) {
	m := newDocument(pagesize.A4, false, title)
	cutLine := row.New(4).Add(col.New(gridSize).Add(line.New(props.Line{Color: grey, Thickness: 0.3, Style: linestyle.Dashed})))

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
	return render(m)
}

func render(m core.Maroto) ([]byte, error) {
	doc, err := m.Generate()
	if err != nil {
		return nil, err
	}
	return doc.GetBytes(), nil
}

// accentColor is the theme's accent, or black.
func (t Theme) accentColor() *props.Color {
	if t.Accent == nil {
		return black
	}
	return &props.Color{Red: t.Accent.R, Green: t.Accent.G, Blue: t.Accent.B}
}

// letterhead is the shared header: optional logo, the title in bold, the
// address lines and phone, then an accent rule.
func letterhead(th Theme, titleSize float64) []core.Row {
	var rows []core.Row

	title := text.New(th.HeaderText, props.Text{Size: titleSize, Style: fontstyle.Bold, Align: align.Center, Color: th.accentColor()})
	if len(th.Logo) > 0 {
		ext := extension.Png
		if th.LogoExt == "jpg" || th.LogoExt == "jpeg" {
			ext = extension.Jpg
		}
		rows = append(rows, row.New(titleSize*0.6).Add(
			col.New(15).Add(image.NewFromBytes(th.Logo, ext, props.Rect{Center: true, Percent: 90})),
			col.New(70).Add(title),
			col.New(15),
		))
	} else {
		rows = append(rows, row.New(titleSize*0.6).Add(col.New(gridSize).Add(title)))
	}

	for _, l := range th.AddressLines {
		rows = append(rows, textRow(4.5, l, props.Text{Size: 9, Align: align.Center}))
	}
	if th.Phone != "" {
		rows = append(rows, textRow(4.5, "Tel: "+th.Phone, props.Text{Size: 9, Align: align.Center}))
	}

	rows = append(rows, row.New(3).Add(col.New(gridSize).Add(line.New(props.Line{Color: th.accentColor(), Thickness: 0.6, OffsetPercent: 50}))))
	return rows
}

// textRow is a full-width row holding one text.
func textRow(height float64, s string, p props.Text) core.Row {
	return row.New(height).Add(col.New(gridSize).Add(text.New(s, p)))
}

// labelValueRow is "Label ........ value" with the value underlined by a
// rule, like a filled-in paper form.
func labelValueRow(label, value string, labelWidth int, bold bool) core.Row {
	valueStyle := fontstyle.Normal
	if bold {
		valueStyle = fontstyle.Bold
	}
	return row.New(7).Add(
		col.New(labelWidth).Add(text.New(label, props.Text{Size: 10, Top: 1.5})),
		col.New(gridSize-labelWidth).Add(
			text.New(value, props.Text{Size: 10, Top: 1.5, Left: 1, Style: valueStyle}),
			line.New(props.Line{Color: grey, Thickness: 0.2, OffsetPercent: 95}),
		),
	)
}

// spacer is an empty row.
func spacer(height float64) core.Row {
	return row.New(height)
}
