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
//
// Page setup, layout and text-row primitives live in internal/shared/pdf
// (sharedpdf below) — this package holds only what's specific to Willcoll's
// domain: the property Theme, the letterhead built from it, and payment
// particulars.
package pdf

import (
	"fmt"

	"github.com/johnfercher/maroto/v2/pkg/components/col"
	"github.com/johnfercher/maroto/v2/pkg/components/image"
	"github.com/johnfercher/maroto/v2/pkg/components/line"
	"github.com/johnfercher/maroto/v2/pkg/components/row"
	"github.com/johnfercher/maroto/v2/pkg/components/text"
	"github.com/johnfercher/maroto/v2/pkg/consts/align"
	"github.com/johnfercher/maroto/v2/pkg/consts/extension"
	"github.com/johnfercher/maroto/v2/pkg/consts/fontstyle"
	"github.com/johnfercher/maroto/v2/pkg/core"
	"github.com/johnfercher/maroto/v2/pkg/props"

	sharedpdf "github.com/codercollo/willcoll/backend/internal/shared/pdf"
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

// accentColor is the theme's accent, or black.
func (t Theme) accentColor() *props.Color {
	if t.Accent == nil {
		return sharedpdf.Black
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
		rows = append(rows, row.New(titleSize*0.6).Add(col.New(sharedpdf.GridSize).Add(title)))
	}

	for _, l := range th.AddressLines {
		rows = append(rows, sharedpdf.TextRow(4.5, l, props.Text{Size: 9, Align: align.Center}))
	}
	if th.Phone != "" {
		rows = append(rows, sharedpdf.TextRow(4.5, "Tel: "+th.Phone, props.Text{Size: 9, Align: align.Center}))
	}

	rows = append(rows, row.New(3).Add(col.New(sharedpdf.GridSize).Add(line.New(props.Line{Color: th.accentColor(), Thickness: 0.6, OffsetPercent: 50}))))
	return rows
}
