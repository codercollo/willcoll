package data

import (
	"reflect"
	"sort"
	"strings"
	"testing"
)

func TestGenerateQRToken(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 2000; i++ {
		tok, err := GenerateQRToken()
		if err != nil {
			t.Fatal(err)
		}
		if len(tok) < 10 || len(tok) != QRTokenLength {
			t.Fatalf("token %q has length %d, want %d (and at least 10)", tok, len(tok), QRTokenLength)
		}
		if strings.ContainsAny(tok, "01OIL") || tok != strings.ToUpper(tok) {
			t.Fatalf("token %q has an ambiguous or lower-case character", tok)
		}
		if !ValidQRCode(tok) {
			t.Fatalf("a generated token %q is not valid", tok)
		}
		if seen[tok] {
			t.Fatalf("token %q repeated within 2000 draws", tok)
		}
		seen[tok] = true
	}
}

func TestGenerateQRTokenUsesTheWholeAlphabet(t *testing.T) {
	counts := map[rune]int{}
	for i := 0; i < 3000; i++ {
		tok, _ := GenerateQRToken()
		for _, r := range tok {
			counts[r]++
		}
	}
	if len(counts) != len(qrTokenAlphabet) {
		t.Errorf("%d distinct characters drawn, want all %d", len(counts), len(qrTokenAlphabet))
	}
	// 36000 characters over 31 symbols is about 1161 each: a strong bias would show.
	for r, n := range counts {
		if n < 900 || n > 1450 {
			t.Errorf("character %q drawn %d times, expected about 1160 (modulo bias?)", r, n)
		}
	}
}

func TestNormalizeQRCode(t *testing.T) {
	for in, want := range map[string]string{
		"K7QM-2XH9-PTRB":    "K7QM2XH9PTRB",
		"k7qm-2xh9-ptrb":    "K7QM2XH9PTRB",
		"  k7qm 2xh9 ptrb ": "K7QM2XH9PTRB",
		"K7QM2XH9PTRB":      "K7QM2XH9PTRB",
		"":                  "",
	} {
		if got := NormalizeQRCode(in); got != want {
			t.Errorf("NormalizeQRCode(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestValidQRCode(t *testing.T) {
	for in, want := range map[string]bool{
		"K7QM2XH9PTRB":  true,
		"K7QM2XH9PTR":   false, // too short
		"K7QM2XH9PTRBC": false, // too long
		"K7QM2XH9PTR0":  false, // 0 is not in the alphabet
		"K7QM2XH9PTRO":  false,
		"K7QM2XH9PTRI":  false,
		"K7QM2XH9PTRL":  false,
		"k7qm2xh9ptrb":  false, // must be normalised first
		"K7QM-2XH9-PTR": false,
	} {
		if got := ValidQRCode(in); got != want {
			t.Errorf("ValidQRCode(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestFormatQRShortCode(t *testing.T) {
	if got := FormatQRShortCode("K7QM2XH9PTRB"); got != "K7QM-2XH9-PTRB" {
		t.Errorf("FormatQRShortCode = %q, want K7QM-2XH9-PTRB", got)
	}
	// What is printed must normalise back to the token.
	if got := NormalizeQRCode(FormatQRShortCode("K7QM2XH9PTRB")); got != "K7QM2XH9PTRB" {
		t.Errorf("round trip = %q", got)
	}
}

func TestNormalizeUnitLabel(t *testing.T) {
	for in, want := range map[string]string{
		"SHOP NO.1": "SHOP1",
		"A1":        "A1",
		"g1":        "G1",
		" b 2 ":     "B2",
	} {
		if got := NormalizeUnitLabel(in); got != want {
			t.Errorf("NormalizeUnitLabel(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFormatUnitQRCodes(t *testing.T) {
	scan, short := FormatUnitQRCodes("K7QM2XH9", "A1")
	if scan != "A1-K7QM2XH9" || short != "A1-K7QM-2XH9" {
		t.Errorf("scan=%q short=%q, want A1-K7QM2XH9 / A1-K7QM-2XH9", scan, short)
	}
	scan, short = FormatUnitQRCodes("K7QM2XH9", "SHOP NO.1")
	if scan != "SHOP1-K7QM2XH9" || short != "SHOP1-K7QM-2XH9" {
		t.Errorf("scan=%q short=%q, want SHOP1-K7QM2XH9 / SHOP1-K7QM-2XH9", scan, short)
	}
	// A legacy (12-character) token has no unit prefix at all.
	scan, short = FormatUnitQRCodes("RS985RVP7TMH", "A1")
	if scan != "RS985RVP7TMH" || short != "RS98-5RVP-7TMH" {
		t.Errorf("legacy scan=%q short=%q, want unprefixed", scan, short)
	}
}

func TestNaturalLess(t *testing.T) {
	units := []string{"A10", "A2", "A1", "B1", "SHOP2", "SHOP10", "G3", "G1"}
	sort.Slice(units, func(i, j int) bool { return naturalLess(units[i], units[j]) })
	want := []string{"A1", "A2", "A10", "B1", "G1", "G3", "SHOP2", "SHOP10"}
	if !reflect.DeepEqual(units, want) {
		t.Errorf("natural sort = %v, want %v", units, want)
	}
}
