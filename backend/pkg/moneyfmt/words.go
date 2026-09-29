package moneyfmt

import "strings"

var (
	ones = []string{"", "One", "Two", "Three", "Four", "Five", "Six", "Seven", "Eight", "Nine",
		"Ten", "Eleven", "Twelve", "Thirteen", "Fourteen", "Fifteen", "Sixteen", "Seventeen", "Eighteen", "Nineteen"}
	tens = []string{"", "", "Twenty", "Thirty", "Forty", "Fifty", "Sixty", "Seventy", "Eighty", "Ninety"}

	scales = []struct {
		value int64
		name  string
	}{
		{1_000_000_000, "Billion"},
		{1_000_000, "Million"},
		{1_000, "Thousand"},
	}
)

// Words spells m out the way a receipt's "The sum shillings" line is
// written by hand: "Fifteen Thousand Five Hundred Shillings Only", or
// "One Hundred Shillings and Fifty Cents Only". Negative amounts are
// spelled as their magnitude; receipts never carry a negative sum.
func (m Money) Words() string {
	cents := m.cents
	if cents < 0 {
		cents = -cents
	}
	shillings, rem := cents/100, cents%100

	var b strings.Builder
	if shillings == 0 {
		b.WriteString("Zero")
	} else {
		b.WriteString(intWords(shillings))
	}
	if shillings == 1 {
		b.WriteString(" Shilling")
	} else {
		b.WriteString(" Shillings")
	}

	if rem > 0 {
		b.WriteString(" and ")
		b.WriteString(intWords(rem))
		b.WriteString(" Cents")
	}
	b.WriteString(" Only")
	return b.String()
}

// intWords spells a positive integer below one trillion.
func intWords(n int64) string {
	var parts []string
	for _, s := range scales {
		if n >= s.value {
			parts = append(parts, belowThousand(n/s.value)+" "+s.name)
			n %= s.value
		}
	}
	if n > 0 {
		parts = append(parts, belowThousand(n))
	}
	return strings.Join(parts, " ")
}

func belowThousand(n int64) string {
	var parts []string
	if n >= 100 {
		parts = append(parts, ones[n/100]+" Hundred")
		n %= 100
	}
	switch {
	case n >= 20:
		if n%10 == 0 {
			parts = append(parts, tens[n/10])
		} else {
			parts = append(parts, tens[n/10]+"-"+ones[n%10])
		}
	case n > 0:
		parts = append(parts, ones[n])
	}
	return strings.Join(parts, " ")
}
