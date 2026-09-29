// Package moneyfmt holds the Money and Period value types shared by the
// API, the data layer and the PDF templates.
//
// Money mirrors Postgres numeric(12,2) exactly: it is an int64 count of
// cents, never a float64, so ledger arithmetic (sums, zero-sum checks,
// subset-sum matching) is exact. On the wire it is a plain decimal string
// ("1900.00") — no thousands separators, no currency prefix — so clients
// can do exact arithmetic on it too. Display formatting for printed
// documents ("1,900.00") lives in Display, never in JSON.
package moneyfmt

import (
	"database/sql/driver"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// maxCents is the largest magnitude numeric(12,2) can hold: 9,999,999,999.99.
const maxCents = 999_999_999_999

var (
	// ErrInvalidMoney is returned when a value is not a plain decimal
	// amount with at most two decimal places.
	ErrInvalidMoney = errors.New("must be a decimal amount with at most 2 decimal places, e.g. \"1900.00\"")

	// ErrMoneyOutOfRange is returned when a value doesn't fit numeric(12,2).
	ErrMoneyOutOfRange = errors.New("amount is out of range")
)

// Money is an exact amount in Kenyan shillings, stored as cents.
type Money struct {
	cents int64
}

// FromCents returns the Money value for a whole number of cents.
func FromCents(cents int64) Money {
	return Money{cents: cents}
}

// Parse parses a plain decimal string such as "1900", "1900.5", "-12.34".
// It rejects thousands separators, exponents, signs other than a single
// leading '-', and more than two decimal places.
func Parse(s string) (Money, error) {
	if s == "" {
		return Money{}, ErrInvalidMoney
	}

	neg := false
	if s[0] == '-' {
		neg = true
		s = s[1:]
	}

	whole, frac, hasPoint := strings.Cut(s, ".")
	if whole == "" || (hasPoint && (frac == "" || len(frac) > 2)) {
		return Money{}, ErrInvalidMoney
	}
	if !allDigits(whole) || !allDigits(frac) {
		return Money{}, ErrInvalidMoney
	}
	if len(frac) == 1 {
		frac += "0"
	} else if frac == "" {
		frac = "00"
	}

	// 13 digits of whole shillings would already exceed numeric(12,2);
	// checking length first also keeps ParseInt away from int64 overflow.
	if len(strings.TrimLeft(whole, "0")) > 10 {
		return Money{}, ErrMoneyOutOfRange
	}

	cents, err := strconv.ParseInt(whole+frac, 10, 64)
	if err != nil {
		return Money{}, ErrInvalidMoney
	}
	if cents > maxCents {
		return Money{}, ErrMoneyOutOfRange
	}
	if neg {
		cents = -cents
	}

	return Money{cents: cents}, nil
}

func allDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// Cents returns the amount as a whole number of cents.
func (m Money) Cents() int64 { return m.cents }

// Add returns m + o.
func (m Money) Add(o Money) Money { return Money{cents: m.cents + o.cents} }

// Sub returns m - o.
func (m Money) Sub(o Money) Money { return Money{cents: m.cents - o.cents} }

// Neg returns -m.
func (m Money) Neg() Money { return Money{cents: -m.cents} }

// PercentOf returns m * hundredths / 10000, i.e. a percentage given in
// hundredths of a percent (5.25% is 525), rounded half away from zero to
// the cent. Integer arithmetic throughout: money never touches a float.
func (m Money) PercentOf(hundredths int64) Money {
	n := m.cents * hundredths
	if n >= 0 {
		return Money{cents: (n + 5000) / 10000}
	}
	return Money{cents: (n - 5000) / 10000}
}

// Cmp returns -1, 0 or +1 as m is less than, equal to or greater than o.
func (m Money) Cmp(o Money) int {
	switch {
	case m.cents < o.cents:
		return -1
	case m.cents > o.cents:
		return 1
	default:
		return 0
	}
}

// IsZero reports whether m == 0.
func (m Money) IsZero() bool { return m.cents == 0 }

// IsPositive reports whether m > 0.
func (m Money) IsPositive() bool { return m.cents > 0 }

// IsNegative reports whether m < 0.
func (m Money) IsNegative() bool { return m.cents < 0 }

// String returns the plain decimal form, e.g. "1900.00" or "-12.34".
func (m Money) String() string {
	return m.format(false)
}

// Display returns the thousands-grouped form used on printed documents,
// e.g. "1,900.00". Never use it for JSON or anything parsed back.
func (m Money) Display() string {
	return m.format(true)
}

func (m Money) format(group bool) string {
	c := m.cents
	sign := ""
	if c < 0 {
		sign = "-"
		c = -c
	}

	whole := strconv.FormatInt(c/100, 10)
	if group {
		for i := len(whole) - 3; i > 0; i -= 3 {
			whole = whole[:i] + "," + whole[i:]
		}
	}

	return fmt.Sprintf("%s%s.%02d", sign, whole, c%100)
}

// MarshalJSON encodes m as a JSON string, e.g. "1900.00".
func (m Money) MarshalJSON() ([]byte, error) {
	return []byte(strconv.Quote(m.String())), nil
}

// UnmarshalJSON accepts either a JSON string ("1900.50") or a JSON number
// (1900.5). Numbers are parsed from their literal text, never through
// float64, so they're exact. JSON null leaves m unchanged.
func (m *Money) UnmarshalJSON(b []byte) error {
	s := string(b)
	if s == "null" {
		return nil
	}

	if unquoted, err := strconv.Unquote(s); err == nil {
		s = unquoted
	}

	v, err := Parse(s)
	if err != nil {
		return err
	}

	*m = v
	return nil
}

// Scan implements sql.Scanner for numeric columns, which the pgx stdlib
// driver returns as text.
func (m *Money) Scan(src any) error {
	var s string

	switch v := src.(type) {
	case string:
		s = v
	case []byte:
		s = string(v)
	case int64:
		if v > maxCents/100 || v < -maxCents/100 {
			return ErrMoneyOutOfRange
		}
		*m = Money{cents: v * 100}
		return nil
	case nil:
		return errors.New("moneyfmt: cannot scan NULL into Money")
	default:
		return fmt.Errorf("moneyfmt: cannot scan %T into Money", src)
	}

	v, err := Parse(s)
	if err != nil {
		return fmt.Errorf("moneyfmt: scanning %q: %w", s, err)
	}

	*m = v
	return nil
}

// Value implements driver.Valuer; Postgres casts the decimal text to
// numeric without loss.
func (m Money) Value() (driver.Value, error) {
	return m.String(), nil
}
