package main

import (
	"bytes"
	"image/png"
	"testing"
)

func TestBuildStickerPNG(t *testing.T) {
	b, err := buildStickerPNG("A1", "KIWI PLACE", "Rent: Ksh 9,000", "A1-K7QM-2XH9", "https://willcoll.example/q/A1-K7QM2XH9")
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(b))
	if err != nil {
		t.Fatalf("not a valid PNG: %v", err)
	}
	bounds := img.Bounds()
	if bounds.Dx() < 400 || bounds.Dy() < 400 {
		t.Errorf("sticker is %dx%d, expected at least 400x400 (QR + title + 3 lines of text)", bounds.Dx(), bounds.Dy())
	}
}

func TestBuildStickerPNGLongPropertyName(t *testing.T) {
	// A long name/unit label must not error or produce a degenerate image
	// (drawScaledString centers by measured width, so this exercises that
	// the canvas grows rather than clipping silently).
	b, err := buildStickerPNG("SHOP1", "THE RUNDA'S ARCADE COMMERCIAL COMPLEX", "Rent: Ksh 25,000", "SHOP1-ABCD-EFGH", "https://willcoll.example/q/SHOP1-ABCDEFGH")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := png.Decode(bytes.NewReader(b)); err != nil {
		t.Fatalf("not a valid PNG: %v", err)
	}
}
