package moneyfmt

import (
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestParse(t *testing.T) {
	tests := []struct {
		in      string
		cents   int64
		wantErr error
	}{
		{"1900", 190000, nil},
		{"1900.5", 190050, nil},
		{"1900.50", 190050, nil},
		{"0.01", 1, nil},
		{"-12.34", -1234, nil},
		{"0", 0, nil},
		{"007.10", 710, nil},
		{"9999999999.99", maxCents, nil},
		{"10000000000.00", 0, ErrMoneyOutOfRange},
		{"99999999999999999999", 0, ErrMoneyOutOfRange},
		{"", 0, ErrInvalidMoney},
		{"-", 0, ErrInvalidMoney},
		{"1,900.00", 0, ErrInvalidMoney},
		{"1900.", 0, ErrInvalidMoney},
		{".50", 0, ErrInvalidMoney},
		{"1900.505", 0, ErrInvalidMoney},
		{"1e3", 0, ErrInvalidMoney},
		{"+5", 0, ErrInvalidMoney},
		{"--5", 0, ErrInvalidMoney},
		{" 5", 0, ErrInvalidMoney},
		{"Kshs 5", 0, ErrInvalidMoney},
	}

	for _, tt := range tests {
		got, err := Parse(tt.in)
		if !errors.Is(err, tt.wantErr) {
			t.Errorf("Parse(%q) err = %v, want %v", tt.in, err, tt.wantErr)
			continue
		}
		if err == nil && got.Cents() != tt.cents {
			t.Errorf("Parse(%q) = %d cents, want %d", tt.in, got.Cents(), tt.cents)
		}
	}
}

func TestFormat(t *testing.T) {
	tests := []struct {
		cents            int64
		plain, displayed string
	}{
		{0, "0.00", "0.00"},
		{5, "0.05", "0.05"},
		{190000, "1900.00", "1,900.00"},
		{-360000, "-3600.00", "-3,600.00"},
		{123456789, "1234567.89", "1,234,567.89"},
		{100000, "1000.00", "1,000.00"},
		{99999, "999.99", "999.99"},
	}

	for _, tt := range tests {
		m := FromCents(tt.cents)
		if got := m.String(); got != tt.plain {
			t.Errorf("FromCents(%d).String() = %q, want %q", tt.cents, got, tt.plain)
		}
		if got := m.Display(); got != tt.displayed {
			t.Errorf("FromCents(%d).Display() = %q, want %q", tt.cents, got, tt.displayed)
		}
	}
}

func TestMoneyJSON(t *testing.T) {
	var v struct {
		A Money  `json:"a"`
		B Money  `json:"b"`
		C *Money `json:"c"`
	}

	if err := json.Unmarshal([]byte(`{"a":"1100.50","b":300,"c":null}`), &v); err != nil {
		t.Fatal(err)
	}
	if v.A.Cents() != 110050 || v.B.Cents() != 30000 || v.C != nil {
		t.Fatalf("unexpected decode: a=%v b=%v c=%v", v.A, v.B, v.C)
	}

	out, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"a":"1100.50","b":"300.00","c":null}`; string(out) != want {
		t.Errorf("Marshal = %s, want %s", out, want)
	}

	for _, bad := range []string{`{"a":"1,100"}`, `{"a":1.005}`, `{"a":true}`, `{"a":1e3}`} {
		if err := json.Unmarshal([]byte(bad), &v); err == nil {
			t.Errorf("Unmarshal(%s) succeeded, want error", bad)
		}
	}
}

func TestMoneyArithmetic(t *testing.T) {
	a, b := FromCents(1000), FromCents(250)
	if got := a.Add(b).Cents(); got != 1250 {
		t.Errorf("Add = %d", got)
	}
	if got := b.Sub(a); got.Cents() != -750 || !got.IsNegative() {
		t.Errorf("Sub = %d", got.Cents())
	}
	if a.Cmp(b) != 1 || b.Cmp(a) != -1 || a.Cmp(a) != 0 {
		t.Error("Cmp mismatch")
	}
	if !a.Neg().Add(a).IsZero() {
		t.Error("Neg mismatch")
	}
}

func TestMoneySQL(t *testing.T) {
	var m Money
	for _, src := range []any{"1900.50", []byte("1900.50")} {
		if err := m.Scan(src); err != nil || m.Cents() != 190050 {
			t.Errorf("Scan(%v) = %d, %v", src, m.Cents(), err)
		}
	}
	if err := m.Scan(int64(7)); err != nil || m.Cents() != 700 {
		t.Errorf("Scan(int64) = %d, %v", m.Cents(), err)
	}
	for _, src := range []any{nil, 1.5, "abc"} {
		if err := m.Scan(src); err == nil {
			t.Errorf("Scan(%v) succeeded, want error", src)
		}
	}

	v, _ := FromCents(-1234).Value()
	if v != "-12.34" {
		t.Errorf("Value = %v", v)
	}
}

func TestPeriod(t *testing.T) {
	p, err := ParsePeriod("2026-09")
	if err != nil || p.Year != 2026 || p.Month != time.September {
		t.Fatalf("ParsePeriod = %+v, %v", p, err)
	}
	if p.String() != "2026-09" || p.Next().String() != "2026-10" || p.Prev().String() != "2026-08" {
		t.Errorf("String/Next/Prev = %s %s %s", p, p.Next(), p.Prev())
	}
	if got := (Period{2026, time.December}).Next().String(); got != "2027-01" {
		t.Errorf("year rollover = %s", got)
	}

	for _, bad := range []string{"", "2026-9", "2026-13", "2026-09-01", "Sep 2026"} {
		if _, err := ParsePeriod(bad); !errors.Is(err, ErrInvalidPeriod) {
			t.Errorf("ParsePeriod(%q) err = %v", bad, err)
		}
	}

	var v struct {
		P Period `json:"p"`
	}
	if err := json.Unmarshal([]byte(`{"p":"2026-09"}`), &v); err != nil || v.P != p {
		t.Errorf("Unmarshal = %+v, %v", v.P, err)
	}
	out, _ := json.Marshal(v)
	if string(out) != `{"p":"2026-09"}` {
		t.Errorf("Marshal = %s", out)
	}
	if err := json.Unmarshal([]byte(`{"p":202609}`), &v); err == nil {
		t.Error("Unmarshal(number) succeeded, want error")
	}

	var s Period
	if err := s.Scan(time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)); err != nil || s != p {
		t.Errorf("Scan(time) = %+v, %v", s, err)
	}
	if err := s.Scan("2026-09-01"); err != nil || s != p {
		t.Errorf("Scan(string) = %+v, %v", s, err)
	}
	dv, _ := p.Value()
	if !dv.(time.Time).Equal(time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("Value = %v", dv)
	}
}

func TestWords(t *testing.T) {
	tests := []struct {
		cents int64
		want  string
	}{
		{0, "Zero Shillings Only"},
		{100, "One Shilling Only"},
		{1_550_000, "Fifteen Thousand Five Hundred Shillings Only"},
		{2_100_000, "Twenty-One Thousand Shillings Only"},
		{10_050, "One Hundred Shillings and Fifty Cents Only"},
		{1_234_567_899, "Twelve Million Three Hundred Forty-Five Thousand Six Hundred Seventy-Eight Shillings and Ninety-Nine Cents Only"},
		{-30_000, "Three Hundred Shillings Only"},
	}
	for _, tt := range tests {
		if got := FromCents(tt.cents).Words(); got != tt.want {
			t.Errorf("FromCents(%d).Words() = %q, want %q", tt.cents, got, tt.want)
		}
	}
}

func TestPercentOf(t *testing.T) {
	tests := []struct {
		amount     int64
		hundredths int64
		want       int64
	}{
		{1650000, 1000, 165000},   // 10% of 16,500.00
		{1000, 0, 0},              // 0%
		{100, 525, 5},             // 5.25% of 1.00 = 0.0525 -> 0.05
		{99, 5000, 50},            // 50% of 0.99 = 0.495 rounds half up
		{-99, 5000, -50},          // half away from zero
		{1234567, 10000, 1234567}, // 100%
	}
	for _, tc := range tests {
		if got := FromCents(tc.amount).PercentOf(tc.hundredths).Cents(); got != tc.want {
			t.Errorf("%d cents at %d/10000 = %d, want %d", tc.amount, tc.hundredths, got, tc.want)
		}
	}
}

// TestMoneyJSONEdgeCases pins down how amounts behave at the API boundary
// (appendix 21.4-21.5): numbers are read from their literal text, never
// through float64, and anything ambiguous is refused rather than guessed.
func TestMoneyJSONEdgeCases(t *testing.T) {
	accept := map[string]int64{
		`"0"`: 0, `0`: 0, `"0.00"`: 0, `-0`: 0, `"-0.00"`: 0, // zero is zero, however spelt
		`"007.50"`: 750, `"1.5"`: 150, `1.50`: 150, `0.1`: 10, `19.99`: 1999,
		`-5`: -500, `"-5"`: -500,
		`"9999999999.99"`: 999999999999, // the numeric(12,2) ceiling
	}
	for in, want := range accept {
		var m Money
		if err := json.Unmarshal([]byte(in), &m); err != nil || m.Cents() != want {
			t.Errorf("Unmarshal(%s) = %d, %v; want %d", in, m.Cents(), err, want)
		}
	}

	for _, in := range []string{
		`""`, `" "`, `"1,000"`, `"1e2"`, `1e2`, `"+5"`, `"5."`, `".5"`, `"NaN"`, `"Infinity"`,
		`"null"`, `"1.005"`, `1.005`, `[]`, `{}`, `true`,
		`12345678901234.56`, // beyond numeric(12,2): refused, not rounded or wrapped
	} {
		m := FromCents(12345) // a failed parse must not clobber the existing value
		if err := json.Unmarshal([]byte(in), &m); err == nil {
			t.Errorf("Unmarshal(%s) succeeded with %s, want an error", in, m)
		} else if m.Cents() != 12345 {
			t.Errorf("Unmarshal(%s) failed but changed the value to %s", in, m)
		}
	}

	// The classic float64 trap: 0.1 + 0.2. In cents it is exactly 0.30.
	var a, b Money
	_ = json.Unmarshal([]byte(`0.1`), &a)
	_ = json.Unmarshal([]byte(`0.2`), &b)
	if got := a.Add(b).String(); got != "0.30" {
		t.Errorf("0.1 + 0.2 = %s, want 0.30", got)
	}
}

// TestMoneyPartialUpdateStates is the PATCH convention (Greenlight ch.8.1): a
// pointer field distinguishes "not sent" from "sent as zero". Omitted and null
// both leave the field nil, meaning unchanged; "0" is a real zero.
func TestMoneyPartialUpdateStates(t *testing.T) {
	type patch struct {
		Rate *Money `json:"rate"`
	}
	tests := []struct {
		name, body string
		wantNil    bool
		wantCents  int64
	}{
		{"omitted", `{}`, true, 0},
		{"null", `{"rate":null}`, true, 0},
		{"zero string", `{"rate":"0"}`, false, 0},
		{"zero number", `{"rate":0}`, false, 0},
		{"a value", `{"rate":"150.50"}`, false, 15050},
	}
	for _, tc := range tests {
		var p patch
		if err := json.Unmarshal([]byte(tc.body), &p); err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if (p.Rate == nil) != tc.wantNil || (p.Rate != nil && p.Rate.Cents() != tc.wantCents) {
			t.Errorf("%s: Rate = %v, want nil=%v cents=%d", tc.name, p.Rate, tc.wantNil, tc.wantCents)
		}
	}

	// A required (non-pointer) amount sent as null is the zero value: the
	// handler's validation, not the decoder, decides whether zero is allowed.
	var required struct {
		Fee Money `json:"fee"`
	}
	if err := json.Unmarshal([]byte(`{"fee":null}`), &required); err != nil || !required.Fee.IsZero() {
		t.Errorf("null into a required amount = %v, %v; want the zero value", required.Fee, err)
	}
}
