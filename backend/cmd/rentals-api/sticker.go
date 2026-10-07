package main

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"strings"

	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"
)

// stickerCanvasWidth is the sticker PNG's fixed width in pixels; everything
// (QR module size, text) is sized to fit it.
const stickerCanvasWidth = 500

// drawScaledString draws s with basicfont.Face7x13 scaled up by an integer
// factor (nearest-neighbor pixel replication — no TTF file needed, so this
// has no dependency on fonts being installed wherever the API runs),
// centered horizontally at the given top y. Returns the height consumed.
func drawScaledString(dst draw.Image, s string, scale, y, canvasWidth int, col color.Color) int {
	face := basicfont.Face7x13
	// Measure at 1x first.
	width := font.MeasureString(face, s).Ceil()
	height := face.Metrics().Height.Ceil()

	small := image.NewRGBA(image.Rect(0, 0, max(width, 1), height))
	d := &font.Drawer{Dst: small, Src: image.NewUniform(col), Face: face, Dot: fixed.P(0, face.Metrics().Ascent.Ceil())}
	d.DrawString(s)

	scaledW, scaledH := width*scale, height*scale
	x0 := (canvasWidth - scaledW) / 2
	for sy := 0; sy < scaledH; sy++ {
		for sx := 0; sx < scaledW; sx++ {
			_, _, _, a := small.At(sx/scale, sy/scale).RGBA()
			if a > 0x8000 {
				dst.Set(x0+sx, y+sy, col)
			}
		}
	}
	return scaledH
}

// buildStickerPNG composes a unit's QR sticker: "UNIT <label>" large at
// top, the QR code, then the property name, monthly rent (from the active
// lease — nothing else about the tenant), and the short code, all centered.
// Nothing about the tenant, the balance, or which specific lease is on the
// sticker: only the unit label, the property, the rent figure and the code.
func buildStickerPNG(unitLabel, propertyName, rentLine, shortCode, scanURL string) ([]byte, error) {
	qrPNG, err := renderQRPNG(scanURL)
	if err != nil {
		return nil, err
	}
	qrImg, err := png.Decode(bytes.NewReader(qrPNG))
	if err != nil {
		return nil, err
	}

	const (
		margin     = 24
		titleScale = 4
		lineGap    = 10
		textScale  = 2
	)
	qrSize := qrImg.Bounds().Dx()
	canvasW := stickerCanvasWidth
	if qrSize+2*margin > canvasW {
		canvasW = qrSize + 2*margin
	}

	titleHeight := basicfont.Face7x13.Metrics().Height.Ceil() * titleScale
	lineHeight := basicfont.Face7x13.Metrics().Height.Ceil() * textScale
	lines := []string{strings.ToUpper(propertyName), rentLine, shortCode}
	canvasH := margin + titleHeight + lineGap + qrSize + lineGap + len(lines)*(lineHeight+lineGap) + margin

	canvas := image.NewRGBA(image.Rect(0, 0, canvasW, canvasH))
	draw.Draw(canvas, canvas.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)

	y := margin
	y += drawScaledString(canvas, "UNIT "+unitLabel, titleScale, y, canvasW, color.Black)
	y += lineGap

	qrX := (canvasW - qrSize) / 2
	draw.Draw(canvas, image.Rect(qrX, y, qrX+qrSize, y+qrSize), qrImg, qrImg.Bounds().Min, draw.Src)
	y += qrSize + lineGap

	for _, l := range lines {
		y += drawScaledString(canvas, l, textScale, y, canvasW, color.Black)
		y += lineGap
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, canvas); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
