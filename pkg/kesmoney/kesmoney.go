package kesmoney

import "fmt"

// Amount is a KES fixed-point money value stored as integer cents, to avoid
// the floating-point drift a float64 would introduce into the ledger
// (spec §4). Every internal/money.PaymentTxInput.Amount and friends are this
// same unit — plain int64 cents — Amount just adds arithmetic/formatting.
type Amount int64

// FromCents wraps a raw integer-cents value as an Amount.
func FromCents(cents int64) Amount { return Amount(cents) }

// Cents returns the underlying integer-cents value.
func (a Amount) Cents() int64 { return int64(a) }

func (a Amount) Add(b Amount) Amount { return a + b }
func (a Amount) Sub(b Amount) Amount { return a - b }
func (a Amount) Neg() Amount         { return -a }

func (a Amount) IsZero() bool     { return a == 0 }
func (a Amount) IsPositive() bool { return a > 0 }
func (a Amount) IsNegative() bool { return a < 0 }

// String formats the amount as a KES decimal string with 2 places, e.g.
// "1234.56" or "-5.00" — exactly the text form a Postgres NUMERIC(_,2)
// column parameter expects, so it can be passed straight into a query.
func (a Amount) String() string {
	cents := int64(a)
	sign := ""
	if cents < 0 {
		sign = "-"
		cents = -cents
	}
	return fmt.Sprintf("%s%d.%02d", sign, cents/100, cents%100)
}
