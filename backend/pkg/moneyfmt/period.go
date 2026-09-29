package moneyfmt

import (
	"database/sql/driver"
	"errors"
	"fmt"
	"strconv"
	"time"
)

// ErrInvalidPeriod is returned when a value isn't a "YYYY-MM" billing period.
var ErrInvalidPeriod = errors.New("must be a billing period in YYYY-MM format, e.g. \"2026-09\"")

// Period is a billing month. On the wire it is "2026-09"; in Postgres it is
// a date column holding the 1st of the month (system-design.txt 3.3).
type Period struct {
	Year  int
	Month time.Month
}

// NewPeriod returns the period containing t.
func NewPeriod(t time.Time) Period {
	return Period{Year: t.Year(), Month: t.Month()}
}

// ParsePeriod parses "YYYY-MM".
func ParsePeriod(s string) (Period, error) {
	t, err := time.Parse("2006-01", s)
	if err != nil {
		return Period{}, ErrInvalidPeriod
	}
	return NewPeriod(t), nil
}

// FirstDay returns midnight UTC on the 1st of the period's month.
func (p Period) FirstDay() time.Time {
	return time.Date(p.Year, p.Month, 1, 0, 0, 0, 0, time.UTC)
}

// Next returns the following month.
func (p Period) Next() Period { return NewPeriod(p.FirstDay().AddDate(0, 1, 0)) }

// Prev returns the preceding month.
func (p Period) Prev() Period { return NewPeriod(p.FirstDay().AddDate(0, -1, 0)) }

// IsZero reports whether p is the zero Period.
func (p Period) IsZero() bool { return p.Year == 0 && p.Month == 0 }

// String returns "YYYY-MM".
func (p Period) String() string {
	return fmt.Sprintf("%04d-%02d", p.Year, int(p.Month))
}

// MarshalJSON encodes p as a JSON string, e.g. "2026-09".
func (p Period) MarshalJSON() ([]byte, error) {
	return []byte(strconv.Quote(p.String())), nil
}

// UnmarshalJSON decodes a "YYYY-MM" JSON string. JSON null leaves p
// unchanged.
func (p *Period) UnmarshalJSON(b []byte) error {
	if string(b) == "null" {
		return nil
	}

	s, err := strconv.Unquote(string(b))
	if err != nil {
		return ErrInvalidPeriod
	}

	v, err := ParsePeriod(s)
	if err != nil {
		return err
	}

	*p = v
	return nil
}

// Scan implements sql.Scanner for date columns.
func (p *Period) Scan(src any) error {
	switch v := src.(type) {
	case time.Time:
		*p = NewPeriod(v)
		return nil
	case string:
		return p.scanText(v)
	case []byte:
		return p.scanText(string(v))
	case nil:
		return errors.New("moneyfmt: cannot scan NULL into Period")
	default:
		return fmt.Errorf("moneyfmt: cannot scan %T into Period", src)
	}
}

func (p *Period) scanText(s string) error {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return fmt.Errorf("moneyfmt: scanning %q into Period: %w", s, err)
	}
	*p = NewPeriod(t)
	return nil
}

// Value implements driver.Valuer, storing the 1st of the month.
func (p Period) Value() (driver.Value, error) {
	return p.FirstDay(), nil
}
