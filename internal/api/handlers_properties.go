package api

import (
	"context"
	"encoding/csv"
	"net/http"
	"strings"
	"time"

	"github.com/codercollo/willcoll-sys/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/julienschmidt/httprouter"
)

type createPropertyRequest struct {
	Name             string   `json:"name"`
	Location         string   `json:"location"`
	BrandNote        string   `json:"brand_note"`
	WaterRatePerM3   *float64 `json:"water_rate_per_m3"`
	GarbageFeeFlat   *float64 `json:"garbage_fee_flat"`
	LateFeePctPerDay *float64 `json:"late_fee_pct_per_day"`
	RentDueDay       *int     `json:"rent_due_day"`
}

type createUnitRequest struct {
	UnitLabel     string  `json:"unit_label"`
	UnitType      string  `json:"unit_type"`
	BaseRent      float64 `json:"base_rent"`
	DepositAmount float64 `json:"deposit_amount"`
}

type propertyResponse struct {
	ID               uuid.UUID `json:"id"`
	OrganizationID   uuid.UUID `json:"organization_id"`
	ManagerID        uuid.UUID `json:"manager_id"`
	Name             string    `json:"name"`
	Location         string    `json:"location"`
	BrandNote        *string   `json:"brand_note"`
	WaterRatePerM3   float64   `json:"water_rate_per_m3"`
	GarbageFeeFlat   float64   `json:"garbage_fee_flat"`
	LateFeePctPerDay float64   `json:"late_fee_pct_per_day"`
	RentDueDay       int       `json:"rent_due_day"`
	CreatedAt        time.Time `json:"created_at"`
}

type unitResponse struct {
	ID            uuid.UUID `json:"id"`
	PropertyID    uuid.UUID `json:"property_id"`
	UnitLabel     string    `json:"unit_label"`
	UnitType      *string   `json:"unit_type"`
	BaseRent      float64   `json:"base_rent"`
	DepositAmount float64   `json:"deposit_amount"`
	Status        string    `json:"status"`
}

func (s *Server) propertyExists(ctx context.Context, tx pgx.Tx, propertyID uuid.UUID) (bool, error) {
	var exists bool
	err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM properties WHERE id = $1
		)`,
		propertyID,
	).Scan(&exists)
	return exists, err
}

// listProperties handles GET /v1/properties.
func (s *Server) listProperties(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
	tx, ok := requestTxFromContext(r.Context())
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "the server encountered a problem and could not process your request")
		return
	}

	rows, err := tx.Query(r.Context(), `
		SELECT id, organization_id, manager_id, name, location, brand_note,
		       water_rate_per_m3, garbage_fee_flat, late_fee_pct_per_day, rent_due_day, created_at
		FROM properties
		ORDER BY created_at, name`)
	if err != nil {
		s.logger.Error("list properties", "error", err)
		writeJSONError(w, http.StatusInternalServerError, "the server encountered a problem and could not process your request")
		return
	}
	defer rows.Close()

	properties := make([]propertyResponse, 0)
	for rows.Next() {
		var p propertyResponse
		if err := rows.Scan(
			&p.ID, &p.OrganizationID, &p.ManagerID, &p.Name, &p.Location, &p.BrandNote,
			&p.WaterRatePerM3, &p.GarbageFeeFlat, &p.LateFeePctPerDay, &p.RentDueDay, &p.CreatedAt,
		); err != nil {
			s.logger.Error("scan property", "error", err)
			writeJSONError(w, http.StatusInternalServerError, "the server encountered a problem and could not process your request")
			return
		}
		properties = append(properties, p)
	}
	if err := rows.Err(); err != nil {
		s.logger.Error("iterate properties", "error", err)
		writeJSONError(w, http.StatusInternalServerError, "the server encountered a problem and could not process your request")
		return
	}

	writeJSON(w, http.StatusOK, properties, "data")
}

// createProperty handles POST /v1/properties.
func (s *Server) createProperty(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
	var input createPropertyRequest
	if !readJSON(w, r, &input) {
		return
	}

	claims, _ := claimsFromContext(r.Context())
	tx, ok := requestTxFromContext(r.Context())
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "the server encountered a problem and could not process your request")
		return
	}

	water := 200.0
	if input.WaterRatePerM3 != nil {
		water = *input.WaterRatePerM3
	}
	garbage := 300.0
	if input.GarbageFeeFlat != nil {
		garbage = *input.GarbageFeeFlat
	}
	late := 1.0
	if input.LateFeePctPerDay != nil {
		late = *input.LateFeePctPerDay
	}
	dueDay := 5
	if input.RentDueDay != nil {
		dueDay = *input.RentDueDay
	}

	prop, err := domain.NewProperty(domain.NewPropertyInput{
		OrganizationID:   claims.OrganizationID,
		ManagerID:        claims.UserID,
		Name:             strings.TrimSpace(input.Name),
		Location:         strings.TrimSpace(input.Location),
		BrandNote:        strings.TrimSpace(input.BrandNote),
		WaterRatePerM3:   water,
		GarbageFeeFlat:   garbage,
		LateFeePctPerDay: late,
		RentDueDay:       dueDay,
	})
	if err != nil {
		writeJSONError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}

	p := propertyResponse{
		OrganizationID:   prop.OrganizationID(),
		ManagerID:        prop.ManagerID(),
		Name:             prop.Name(),
		Location:         prop.Location(),
		WaterRatePerM3:   prop.WaterRatePerM3(),
		GarbageFeeFlat:   prop.GarbageFeeFlat(),
		LateFeePctPerDay: prop.LateFeePctPerDay(),
		RentDueDay:       prop.RentDueDay(),
	}
	if prop.BrandNote() != "" {
		brandNote := prop.BrandNote()
		p.BrandNote = &brandNote
	}

	err = tx.QueryRow(r.Context(), `
		INSERT INTO properties (
			organization_id, manager_id, name, location, brand_note,
			water_rate_per_m3, garbage_fee_flat, late_fee_pct_per_day, rent_due_day
		)
		VALUES ($1, $2, $3, $4, NULLIF($5, ''), $6, $7, $8, $9)
		RETURNING id, created_at`,
		p.OrganizationID, p.ManagerID, p.Name, p.Location, prop.BrandNote(),
		p.WaterRatePerM3, p.GarbageFeeFlat, p.LateFeePctPerDay, p.RentDueDay,
	).Scan(&p.ID, &p.CreatedAt)
	if err != nil {
		s.logger.Error("create property", "error", err)
		writeJSONError(w, http.StatusInternalServerError, "the server encountered a problem and could not process your request")
		return
	}

	writeJSON(w, http.StatusCreated, p, "data")
}

// listUnits handles GET /v1/properties/:id/units.
func (s *Server) listUnits(w http.ResponseWriter, r *http.Request, ps httprouter.Params) {
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

	exists, err := s.propertyExists(r.Context(), tx, propertyID)
	if err != nil {
		s.logger.Error("check property", "error", err)
		writeJSONError(w, http.StatusInternalServerError, "the server encountered a problem and could not process your request")
		return
	}
	if !exists {
		writeJSONError(w, http.StatusNotFound, "property not found")
		return
	}

	rows, err := tx.Query(r.Context(), `
		SELECT id, property_id, unit_label, unit_type, base_rent, deposit_amount, status::text
		FROM units
		WHERE property_id = $1
		ORDER BY unit_label`,
		propertyID,
	)
	if err != nil {
		s.logger.Error("list units", "error", err)
		writeJSONError(w, http.StatusInternalServerError, "the server encountered a problem and could not process your request")
		return
	}
	defer rows.Close()

	units := make([]unitResponse, 0)
	for rows.Next() {
		var u unitResponse
		if err := rows.Scan(&u.ID, &u.PropertyID, &u.UnitLabel, &u.UnitType, &u.BaseRent, &u.DepositAmount, &u.Status); err != nil {
			s.logger.Error("scan unit", "error", err)
			writeJSONError(w, http.StatusInternalServerError, "the server encountered a problem and could not process your request")
			return
		}
		units = append(units, u)
	}
	if err := rows.Err(); err != nil {
		s.logger.Error("iterate units", "error", err)
		writeJSONError(w, http.StatusInternalServerError, "the server encountered a problem and could not process your request")
		return
	}

	writeJSON(w, http.StatusOK, units, "data")
}

// createUnit handles POST /v1/properties/:id/units.
func (s *Server) createUnit(w http.ResponseWriter, r *http.Request, ps httprouter.Params) {
	propertyID, err := uuid.Parse(ps.ByName("id"))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid property id")
		return
	}

	var input createUnitRequest
	if !readJSON(w, r, &input) {
		return
	}

	tx, ok := requestTxFromContext(r.Context())
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "the server encountered a problem and could not process your request")
		return
	}

	exists, err := s.propertyExists(r.Context(), tx, propertyID)
	if err != nil {
		s.logger.Error("check property", "error", err)
		writeJSONError(w, http.StatusInternalServerError, "the server encountered a problem and could not process your request")
		return
	}
	if !exists {
		writeJSONError(w, http.StatusNotFound, "property not found")
		return
	}

	unit, err := domain.NewUnit(domain.NewUnitInput{
		PropertyID:    propertyID,
		UnitLabel:     strings.TrimSpace(input.UnitLabel),
		UnitType:      strings.TrimSpace(input.UnitType),
		BaseRent:      input.BaseRent,
		DepositAmount: input.DepositAmount,
		Status:        "vacant",
	})
	if err != nil {
		writeJSONError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}

	if err := validateUnitLabelUnique(r.Context(), tx, propertyID, unit.UnitLabel()); err != nil {
		writeJSONError(w, http.StatusConflict, err.Error())
		return
	}

	var u unitResponse
	err = tx.QueryRow(r.Context(), `
		INSERT INTO units (property_id, unit_label, unit_type, base_rent, deposit_amount, status)
		VALUES ($1, $2, NULLIF($3, ''), $4, $5, 'vacant')
		RETURNING id, property_id, unit_label, unit_type, base_rent, deposit_amount, status::text`,
		unit.PropertyID(), unit.UnitLabel(), unit.UnitType(), unit.BaseRent(), unit.DepositAmount(),
	).Scan(&u.ID, &u.PropertyID, &u.UnitLabel, &u.UnitType, &u.BaseRent, &u.DepositAmount, &u.Status)
	if err != nil {
		if isUniqueViolation(err) {
			writeJSONError(w, http.StatusConflict, ErrUnitLabelTaken.Error())
			return
		}
		s.logger.Error("create unit", "error", err)
		writeJSONError(w, http.StatusInternalServerError, "the server encountered a problem and could not process your request")
		return
	}

	writeJSON(w, http.StatusCreated, u, "data")
}

// importUnitsCSV handles POST /v1/properties/:id/units/import.
func (s *Server) importUnitsCSV(w http.ResponseWriter, r *http.Request, ps httprouter.Params) {
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

	exists, err := s.propertyExists(r.Context(), tx, propertyID)
	if err != nil {
		s.logger.Error("check property", "error", err)
		writeJSONError(w, http.StatusInternalServerError, "the server encountered a problem and could not process your request")
		return
	}
	if !exists {
		writeJSONError(w, http.StatusNotFound, "property not found")
		return
	}

	if err := r.ParseMultipartForm(1 << 20); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid multipart form")
		return
	}

	file, _, err := r.FormFile("file")
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "CSV file is required")
		return
	}
	defer file.Close()

	records, err := csv.NewReader(file).ReadAll()
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid CSV file")
		return
	}
	if len(records) < 2 {
		writeJSONError(w, http.StatusBadRequest, "CSV must have a header row and at least one data row")
		return
	}

	required := []string{"house_no", "unit_type", "base_rent", "deposit_amount"}
	columnIndex := make(map[string]int, len(required))
	for i, name := range records[0] {
		columnIndex[strings.ToLower(strings.TrimSpace(name))] = i
	}
	for _, name := range required {
		if _, ok := columnIndex[name]; !ok {
			writeJSONError(w, http.StatusBadRequest, "CSV header must contain house_no, unit_type, base_rent, and deposit_amount")
			return
		}
	}

	rows := make([]unitCSVRow, 0, len(records)-1)
	for _, record := range records[1:] {
		ordered := make([]string, len(required))
		for i, name := range required {
			ordered[i] = record[columnIndex[name]]
		}
		row, err := validateUnitCSVRow(ordered)
		if err != nil {
			writeJSONError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		rows = append(rows, row)
	}

	if err := validateUnitCSVRows(rows); err != nil {
		writeJSONError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}

	created := make([]unitResponse, 0, len(rows))
	for _, row := range rows {
		if err := validateUnitLabelUnique(r.Context(), tx, propertyID, row.HouseNo); err != nil {
			writeJSONError(w, http.StatusConflict, err.Error())
			return
		}

		var u unitResponse
		err := tx.QueryRow(r.Context(), `
			INSERT INTO units (property_id, unit_label, unit_type, base_rent, deposit_amount, status)
			VALUES ($1, $2, NULLIF($3, ''), $4, $5, 'vacant')
			RETURNING id, property_id, unit_label, unit_type, base_rent, deposit_amount, status::text`,
			propertyID, row.HouseNo, row.UnitType, row.BaseRent, row.DepositAmount,
		).Scan(&u.ID, &u.PropertyID, &u.UnitLabel, &u.UnitType, &u.BaseRent, &u.DepositAmount, &u.Status)
		if err != nil {
			if isUniqueViolation(err) {
				writeJSONError(w, http.StatusConflict, ErrUnitLabelTaken.Error())
				return
			}
			s.logger.Error("import unit", "error", err)
			writeJSONError(w, http.StatusInternalServerError, "the server encountered a problem and could not process your request")
			return
		}
		created = append(created, u)
	}

	writeJSON(w, http.StatusOK, created, "data")
}
