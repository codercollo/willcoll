package pdf

import (
	"github.com/johnfercher/maroto/v2/pkg/components/col"
	"github.com/johnfercher/maroto/v2/pkg/components/image"
	"github.com/johnfercher/maroto/v2/pkg/components/line"
	"github.com/johnfercher/maroto/v2/pkg/components/page"
	"github.com/johnfercher/maroto/v2/pkg/components/row"
	"github.com/johnfercher/maroto/v2/pkg/consts/extension"
	"github.com/johnfercher/maroto/v2/pkg/consts/linestyle"
	"github.com/johnfercher/maroto/v2/pkg/consts/pagesize"
	"github.com/johnfercher/maroto/v2/pkg/core"
	"github.com/johnfercher/maroto/v2/pkg/props"
)

// BuildQRStickerSheet lays pre-rendered sticker PNGs (each already carries
// "UNIT <label>", the QR, the property name, rent and short code — see
// cmd/api's buildStickerPNG) two per A4 portrait page with a dashed cut
// line between them, the same "print, cut, done" layout the other 2-up
// documents (water/garbage invoices) use. Callers sort the input slice
// first — a run through the units in door order.
func BuildQRStickerSheet(pngs [][]byte) ([]byte, error) {
	m := newDocument(pagesize.A4, false, "Unit QR Stickers")
	cutLine := row.New(4).Add(col.New(gridSize).Add(line.New(props.Line{Color: grey, Thickness: 0.3, Style: linestyle.Dashed})))

	stickerRow := func(png []byte) core.Row {
		return row.New(130).Add(col.New(gridSize).Add(image.NewFromBytes(png, extension.Png, props.Rect{Center: true, Percent: 90})))
	}

	var pages []core.Page
	for i := 0; i < len(pngs); i += 2 {
		rows := []core.Row{stickerRow(pngs[i])}
		if i+1 < len(pngs) {
			rows = append(rows, cutLine, stickerRow(pngs[i+1]))
		}
		pages = append(pages, page.New().Add(rows...))
	}
	m.AddPages(pages...)
	return render(m)
}
