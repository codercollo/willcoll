package kesmoney

import "testing"

func TestString(t *testing.T) {
	cases := []struct {
		cents int64
		want  string
	}{
		{0, "0.00"},
		{1, "0.01"},
		{123456, "1234.56"},
		{-500, "-5.00"},
	}
	for _, c := range cases {
		if got := FromCents(c.cents).String(); got != c.want {
			t.Errorf("FromCents(%d).String() = %q, want %q", c.cents, got, c.want)
		}
	}
}

func TestArithmetic(t *testing.T) {
	a := FromCents(1000)
	b := FromCents(300)
	if got := a.Add(b); got != 1300 {
		t.Errorf("Add = %d, want 1300", got)
	}
	if got := a.Sub(b); got != 700 {
		t.Errorf("Sub = %d, want 700", got)
	}
	if !a.Neg().IsNegative() {
		t.Error("Neg() should be negative")
	}
	if !FromCents(0).IsZero() {
		t.Error("FromCents(0) should be zero")
	}
}
