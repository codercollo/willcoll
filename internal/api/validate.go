package api

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode"

	"github.com/codercollo/willcoll-sys/pkg/msisdn"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
)

var (
	ErrUnitLabelRequired     = errors.New("unit label is required")
	ErrUnitLabelTooLong      = errors.New("unit label must be 100 characters or fewer")
	ErrUnitLabelInvalid      = errors.New("unit label must be plain text")
	ErrUnitLabelTaken        = errors.New("unit label already exists for this property")
	ErrCSVInvalidColumnCount = errors.New("CSV row must have house_no, unit_type, base_rent, and deposit_amount")
	ErrCSVInvalidBaseRent    = errors.New("CSV base_rent must be a non-negative number")
	ErrCSVInvalidDeposit     = errors.New("CSV deposit_amount must be a non-negative number")
	ErrCSVDuplicateUnitLabel = errors.New("CSV contains a duplicate house_no")
)

// validateUnitLabel enforces the plain-text, non-empty, length-capped rule for
// a unit's house label.
func validateUnitLabel(label string) error {
	label = strings.TrimSpace(label)
	if label == "" {
		return ErrUnitLabelRequired
	}
	if len(label) > 100 {
		return ErrUnitLabelTooLong
	}
	for _, r := range label {
		if unicode.IsControl(r) {
			return ErrUnitLabelInvalid
		}
	}
	return nil
}

// validateUnitLabelUnique checks the units table for an existing label under
// the same property. The DB UNIQUE (property_id, unit_label) constraint is the
// final backstop; this keeps the common path to a named error.
func validateUnitLabelUnique(ctx context.Context, tx pgx.Tx, propertyID uuid.UUID, label string) error {
	var exists bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM units
			WHERE property_id = $1 AND unit_label = $2
		)`,
		propertyID, label,
	).Scan(&exists); err != nil {
		return fmt.Errorf("check unit label uniqueness: %w", err)
	}
	if exists {
		return ErrUnitLabelTaken
	}
	return nil
}

// unitCSVRow is the validated form of one row in a paper-ledger unit import.
type unitCSVRow struct {
	HouseNo       string
	UnitType      string
	BaseRent      float64
	DepositAmount float64
}

// validateUnitCSVRow validates one CSV record in the fixed import column order:
// house_no, unit_type, base_rent, deposit_amount.
func validateUnitCSVRow(record []string) (unitCSVRow, error) {
	if len(record) != 4 {
		return unitCSVRow{}, ErrCSVInvalidColumnCount
	}

	row := unitCSVRow{
		HouseNo:  strings.TrimSpace(record[0]),
		UnitType: strings.TrimSpace(record[1]),
	}

	if err := validateUnitLabel(row.HouseNo); err != nil {
		return unitCSVRow{}, err
	}

	baseRent, err := strconv.ParseFloat(strings.TrimSpace(record[2]), 64)
	if err != nil || baseRent < 0 {
		return unitCSVRow{}, ErrCSVInvalidBaseRent
	}
	row.BaseRent = baseRent

	depositAmount, err := strconv.ParseFloat(strings.TrimSpace(record[3]), 64)
	if err != nil || depositAmount < 0 {
		return unitCSVRow{}, ErrCSVInvalidDeposit
	}
	row.DepositAmount = depositAmount

	return row, nil
}

// validateUnitCSVRows checks for duplicate house numbers within one import.
func validateUnitCSVRows(rows []unitCSVRow) error {
	seen := make(map[string]struct{}, len(rows))
	for _, row := range rows {
		if _, ok := seen[row.HouseNo]; ok {
			return fmt.Errorf("%w: %s", ErrCSVDuplicateUnitLabel, row.HouseNo)
		}
		seen[row.HouseNo] = struct{}{}
	}
	return nil
}

// Validator is the hand-rolled request validator (LGF §4.5). It accumulates
// field-level errors keyed by JSON field name; failedValidationResponse
// (errors.go) writes Errors verbatim when Valid() returns false.
type Validator struct {
	Errors map[string]string
}

// NewValidator returns an empty Validator ready to accumulate errors.
func NewValidator() *Validator {
	return &Validator{Errors: make(map[string]string)}
}

// Valid reports whether no field-level errors were recorded.
func (v *Validator) Valid() bool {
	return len(v.Errors) == 0
}

// AddError records message for key, keeping only the first error per key.
func (v *Validator) AddError(key, message string) {
	if _, exists := v.Errors[key]; !exists {
		v.Errors[key] = message
	}
}

// Check records message for key when ok is false.
func (v *Validator) Check(ok bool, key, message string) {
	if !ok {
		v.AddError(key, message)
	}
}

// isValidKESAmount reports whether s is a positive KES amount with at most two
// decimal places (spec §6.2).
func isValidKESAmount(s string) bool {
	amount, err := decimal.NewFromString(strings.TrimSpace(s))
	if err != nil {
		return false
	}
	if amount.Sign() <= 0 {
		return false
	}
	return amount.Equal(amount.Truncate(2))
}

// isValidMSISDN reports whether s is a valid Kenyan MSISDN, delegating to
// pkg/msisdn (spec §6.2, §3.1).
func isValidMSISDN(s string) bool {
	return msisdn.Valid(strings.TrimSpace(s))
}

// isMeterReadingMonotonic is a stub for the meter-reading monotonicity rule
// (the current reading must not go backwards). It will delegate to
// domain.MeterReading.Validate once that entity lands in Phase 6; until then
// it enforces the rule inline.
func isMeterReadingMonotonic(previous, current decimal.Decimal) bool {
	return current.GreaterThanOrEqual(previous)
}
