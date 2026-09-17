package api

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/codercollo/willcoll-sys/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/julienschmidt/httprouter"
	"github.com/shopspring/decimal"
)

type createMeterRequest struct {
	MeterType   string `json:"meter_type"`
	MeterNumber string `json:"meter_number"`
}

type meterResponse struct {
	ID          uuid.UUID `json:"id"`
	UnitID      uuid.UUID `json:"unit_id"`
	UnitLabel   string    `json:"unit_label"`
	MeterType   string    `json:"meter_type"`
	MeterNumber *string   `json:"meter_number"`

	// LatestReading is nil when the meter has never had a reading recorded —
	// the frontend's monotonicity hint (spec §"Phase 4.2") has nothing to
	// compare against yet in that case, same as the server-side check.
	LatestReading *struct {
		ReadingMonth   time.Time       `json:"reading_month"`
		CurrentReading decimal.Decimal `json:"current_reading"`
	} `json:"latest_reading"`
}

// createMeter handles POST /v1/units/:id/meters — Manager only. Provisions
// the physical meter a unit's readings will be recorded against; nothing
// else in this codebase creates a meters row.
func (s *Server) createMeter(w http.ResponseWriter, r *http.Request, ps httprouter.Params) {
	unitID, err := uuid.Parse(ps.ByName("id"))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid unit id")
		return
	}

	var input createMeterRequest
	if !readJSON(w, r, &input) {
		return
	}
	if strings.TrimSpace(input.MeterType) == "" {
		writeJSONError(w, http.StatusBadRequest, "meter_type is required")
		return
	}

	tx, ok := requestTxFromContext(r.Context())
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "the server encountered a problem and could not process your request")
		return
	}

	var resp meterResponse
	var meterNumber *string
	err = tx.QueryRow(r.Context(), `
		INSERT INTO meters (unit_id, meter_type, meter_number)
		VALUES ($1, $2::meter_kind, $3)
		RETURNING id, unit_id, meter_type::text, meter_number`,
		unitID, input.MeterType, nullIfEmptyString(input.MeterNumber),
	).Scan(&resp.ID, &resp.UnitID, &resp.MeterType, &meterNumber)
	if err != nil {
		s.logger.Error("create meter", "error", err)
		writeJSONError(w, http.StatusInternalServerError, "the server encountered a problem and could not process your request")
		return
	}
	resp.MeterNumber = meterNumber

	writeJSON(w, http.StatusCreated, resp, "data")
}

func nullIfEmptyString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// listPropertyMeters handles GET /v1/properties/:id/meters — every meter
// across the property's units, with its latest reading if it has one, so
// the frontend can build the reading-entry grid and a real monotonicity
// hint without guessing (spec Phase 4.1-4.2).
func (s *Server) listPropertyMeters(w http.ResponseWriter, r *http.Request, ps httprouter.Params) {
	propertyID, err := uuid.Parse(ps.ByName("id"))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid property id")
		return
	}

	tx, ok := requestTxFromContext(r.Context())
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "the server encountered a problem and could not process your request")
		return
	}

	rows, err := tx.Query(r.Context(), `
		SELECT m.id, m.unit_id, u.unit_label, m.meter_type::text, m.meter_number,
		       lr.reading_month, lr.current_reading
		FROM meters m
		JOIN units u ON u.id = m.unit_id
		LEFT JOIN LATERAL (
			SELECT reading_month, current_reading
			FROM meter_readings
			WHERE meter_id = m.id
			ORDER BY reading_month DESC
			LIMIT 1
		) lr ON true
		WHERE u.property_id = $1
		ORDER BY u.unit_label`,
		propertyID,
	)
	if err != nil {
		s.logger.Error("list property meters", "error", err)
		writeJSONError(w, http.StatusInternalServerError, "the server encountered a problem and could not process your request")
		return
	}
	defer rows.Close()

	out := make([]meterResponse, 0)
	for rows.Next() {
		var m meterResponse
		var readingMonth *time.Time
		var currentReading *decimal.Decimal
		if err := rows.Scan(&m.ID, &m.UnitID, &m.UnitLabel, &m.MeterType, &m.MeterNumber, &readingMonth, &currentReading); err != nil {
			s.logger.Error("scan property meter", "error", err)
			writeJSONError(w, http.StatusInternalServerError, "the server encountered a problem and could not process your request")
			return
		}
		if readingMonth != nil && currentReading != nil {
			m.LatestReading = &struct {
				ReadingMonth   time.Time       `json:"reading_month"`
				CurrentReading decimal.Decimal `json:"current_reading"`
			}{ReadingMonth: *readingMonth, CurrentReading: *currentReading}
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		s.logger.Error("iterate property meters", "error", err)
		writeJSONError(w, http.StatusInternalServerError, "the server encountered a problem and could not process your request")
		return
	}

	writeJSON(w, http.StatusOK, out, "data")
}

type createMeterReadingRequest struct {
	ReadingMonth   string          `json:"reading_month"`
	CurrentReading decimal.Decimal `json:"current_reading"`
}

type meterReadingResponse struct {
	ID              uuid.UUID       `json:"id"`
	MeterID         uuid.UUID       `json:"meter_id"`
	ReadingMonth    time.Time       `json:"reading_month"`
	PreviousReading decimal.Decimal `json:"previous_reading"`
	CurrentReading  decimal.Decimal `json:"current_reading"`
	UnitsConsumed   decimal.Decimal `json:"units_consumed"`
	RatePerM3       decimal.Decimal `json:"rate_per_m3"`
	AmountDue       decimal.Decimal `json:"amount_due"`
	InvoiceID       uuid.UUID       `json:"invoice_id"`
}

// createMeterReading handles POST /v1/meters/:id/readings.
func (s *Server) createMeterReading(w http.ResponseWriter, r *http.Request, ps httprouter.Params) {
	meterID, err := uuid.Parse(ps.ByName("id"))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid meter id")
		return
	}

	var input createMeterReadingRequest
	if !readJSON(w, r, &input) {
		return
	}

	readingMonth, err := time.Parse("2006-01-02", strings.TrimSpace(input.ReadingMonth))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "reading_month must be YYYY-MM-DD")
		return
	}

	claims, _ := claimsFromContext(r.Context())
	tx, ok := requestTxFromContext(r.Context())
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "the server encountered a problem and could not process your request")
		return
	}

	var unitID uuid.UUID
	var propertyID uuid.UUID
	var waterRate float64
	err = tx.QueryRow(r.Context(), `
		SELECT m.unit_id, p.id, p.water_rate_per_m3
		FROM meters m
		JOIN units u ON u.id = m.unit_id
		JOIN properties p ON p.id = u.property_id
		WHERE m.id = $1`,
		meterID,
	).Scan(&unitID, &propertyID, &waterRate)
	if errors.Is(err, pgx.ErrNoRows) {
		writeJSONError(w, http.StatusNotFound, "meter not found")
		return
	}
	if err != nil {
		s.logger.Error("lookup meter", "error", err)
		writeJSONError(w, http.StatusInternalServerError, "the server encountered a problem and could not process your request")
		return
	}

	previousCurrent := decimal.Zero
	var previousReading *domain.MeterReading
	err = tx.QueryRow(r.Context(), `
		SELECT current_reading
		FROM meter_readings
		WHERE meter_id = $1
		ORDER BY reading_month DESC
		LIMIT 1`,
		meterID,
	).Scan(&previousCurrent)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		s.logger.Error("lookup previous reading", "error", err)
		writeJSONError(w, http.StatusInternalServerError, "the server encountered a problem and could not process your request")
		return
	}
	if err == nil {
		previousReading, err = domain.NewMeterReading(domain.NewMeterReadingInput{
			MeterID:         meterID,
			ReadingMonth:    readingMonth,
			PreviousReading: decimal.Zero,
			CurrentReading:  previousCurrent,
			RatePerM3:       decimal.Zero,
			RecordedBy:      claims.UserID,
		})
		if err != nil {
			writeJSONError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
	}

	rate := decimal.NewFromFloat(waterRate)
	reading, err := domain.NewMeterReading(domain.NewMeterReadingInput{
		MeterID:         meterID,
		ReadingMonth:    readingMonth,
		PreviousReading: previousCurrent,
		CurrentReading:  input.CurrentReading,
		RatePerM3:       rate,
		RecordedBy:      claims.UserID,
	})
	if err != nil {
		writeJSONError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	if err := reading.Validate(previousReading); err != nil {
		writeJSONError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}

	var leaseID uuid.UUID
	err = tx.QueryRow(r.Context(), `
		SELECT id
		FROM leases
		WHERE unit_id = $1 AND status = 'active'
		ORDER BY created_at DESC
		LIMIT 1`,
		unitID,
	).Scan(&leaseID)
	if errors.Is(err, pgx.ErrNoRows) {
		writeJSONError(w, http.StatusConflict, "unit has no active lease")
		return
	}
	if err != nil {
		s.logger.Error("lookup active lease", "error", err)
		writeJSONError(w, http.StatusInternalServerError, "the server encountered a problem and could not process your request")
		return
	}

	var resp meterReadingResponse
	err = tx.QueryRow(r.Context(), `
		INSERT INTO meter_readings (
			meter_id, reading_month, previous_reading, current_reading, rate_per_m3, recorded_by
		)
		VALUES ($1, $2, $3::numeric, $4::numeric, $5::numeric, $6)
		RETURNING id, meter_id, reading_month, previous_reading, current_reading,
		          units_consumed, rate_per_m3, amount_due`,
		meterID, reading.ReadingMonth(), reading.PreviousReading().String(),
		reading.CurrentReading().String(), reading.RatePerM3().String(), claims.UserID,
	).Scan(
		&resp.ID, &resp.MeterID, &resp.ReadingMonth, &resp.PreviousReading,
		&resp.CurrentReading, &resp.UnitsConsumed, &resp.RatePerM3, &resp.AmountDue,
	)
	if err != nil {
		s.logger.Error("insert meter reading", "error", err)
		writeJSONError(w, http.StatusInternalServerError, "the server encountered a problem and could not process your request")
		return
	}

	err = tx.QueryRow(r.Context(), `
		INSERT INTO invoices (organization_id, lease_id, period_month, invoice_type, amount_due, meter_reading_id)
		VALUES ($1, $2, $3, 'water_garbage', $4::numeric, $5)
		RETURNING id`,
		claims.OrganizationID, leaseID, reading.ReadingMonth(), resp.AmountDue.String(), resp.ID,
	).Scan(&resp.InvoiceID)
	if err != nil {
		s.logger.Error("insert water invoice", "error", err)
		writeJSONError(w, http.StatusInternalServerError, "the server encountered a problem and could not process your request")
		return
	}

	writeJSON(w, http.StatusCreated, resp, "data")
}
