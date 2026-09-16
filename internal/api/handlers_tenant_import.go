package api

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/codercollo/willcoll-sys/internal/money"
	"github.com/codercollo/willcoll-sys/pkg/msisdn"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/julienschmidt/httprouter"
)

// tenantImportCSVColumns is the order-independent header accepted by the bulk
// tenant/unit/lease import (spec §3.2a).
var tenantImportCSVColumns = []string{
	"unit_label", "unit_type", "base_rent", "deposit_amount",
	"tenant_full_name", "tenant_phone", "tenant_id_number",
	"lease_start_date", "monthly_rent", "rent_due_day", "late_fee_pct_per_day",
}

// tenantImportRow is one parsed (not yet validated) CSV row.
type tenantImportRow struct {
	UnitLabel      string
	UnitType       string
	BaseRent       float64
	DepositAmount  float64
	TenantFullName string
	TenantPhone    string
	TenantIDNumber string
	LeaseStartDate string
	MonthlyRent    string
	RentDueDay     string
	LateFeePctDay  string
}

type tenantImportRowResult struct {
	UnitLabel string `json:"unit_label"`
	Status    string `json:"status"` // "created" | "skipped"
	Reason    string `json:"reason,omitempty"`
}

type tenantImportResponse struct {
	RowCount     int                     `json:"row_count"`
	CreatedCount int                     `json:"created_count"`
	SkippedCount int                     `json:"skipped_count"`
	RowResults   []tenantImportRowResult `json:"row_results"`
}

// importTenantsCSV handles POST /v1/properties/:id/tenants/import (spec §3.2a).
// It onboards units and their sitting tenants (or vacant units) in bulk, and
// records one tenant_import_batches row per upload.
func (s *Server) importTenantsCSV(w http.ResponseWriter, r *http.Request, ps httprouter.Params) {
	propertyID, err := uuid.Parse(ps.ByName("id"))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid property id")
		return
	}

	claims, ok := claimsFromContext(r.Context())
	if !ok {
		writeJSONError(w, http.StatusUnauthorized, "invalid or missing authentication token")
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

	// 409 unless a Landlord is already attached (spec §3.2a step 2).
	hasOwner, err := s.propertyHasOwner(r.Context(), tx, propertyID)
	if err != nil {
		s.logger.Error("check property ownership", "error", err)
		writeJSONError(w, http.StatusInternalServerError, "the server encountered a problem and could not process your request")
		return
	}
	if !hasOwner {
		writeJSONError(w, http.StatusConflict, "attach a landlord to this property before importing tenants")
		return
	}

	var defaults struct {
		RentDueDay       int
		LateFeePctPerDay float64
	}
	if err := tx.QueryRow(r.Context(),
		`SELECT rent_due_day, late_fee_pct_per_day::float8 FROM properties WHERE id = $1`, propertyID,
	).Scan(&defaults.RentDueDay, &defaults.LateFeePctPerDay); err != nil {
		s.logger.Error("read property defaults", "error", err)
		writeJSONError(w, http.StatusInternalServerError, "the server encountered a problem and could not process your request")
		return
	}

	// Parse + validate the whole file before any write (spec §3.2a).
	rows, filename, err := parseTenantCSV(r)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}

	results := make([]tenantImportRowResult, 0, len(rows))
	created := 0
	for _, row := range rows {
		res, err := s.processTenantImportRow(r.Context(), tx, claims.OrganizationID, claims.UserID, propertyID, defaults, row)
		if err != nil {
			results = append(results, tenantImportRowResult{UnitLabel: row.UnitLabel, Status: "skipped", Reason: err.Error()})
			continue
		}
		results = append(results, res)
		if res.Status == "created" {
			created++
		}
	}

	rowResultsJSON, err := json.Marshal(results)
	if err != nil {
		s.logger.Error("marshal row results", "error", err)
		writeJSONError(w, http.StatusInternalServerError, "the server encountered a problem and could not process your request")
		return
	}
	if _, err := tx.Exec(r.Context(), `
		INSERT INTO tenant_import_batches
			(organization_id, property_id, uploaded_by, filename, row_count, created_count, skipped_count, row_results)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		claims.OrganizationID, propertyID, claims.UserID, filename, len(rows), created, len(rows)-created, rowResultsJSON,
	); err != nil {
		s.logger.Error("record import batch", "error", err)
		writeJSONError(w, http.StatusInternalServerError, "the server encountered a problem and could not process your request")
		return
	}

	writeJSON(w, http.StatusOK, tenantImportResponse{
		RowCount:     len(rows),
		CreatedCount: created,
		SkippedCount: len(rows) - created,
		RowResults:   results,
	}, "data")
}

// propertyHasOwner reports whether the property has a property_ownership row
// (i.e. is attached to a Landlord, spec §2.3, §3.2a).
func (s *Server) propertyHasOwner(ctx context.Context, tx pgx.Tx, propertyID uuid.UUID) (bool, error) {
	var exists bool
	err := tx.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM property_ownership WHERE property_id = $1)`, propertyID,
	).Scan(&exists)
	return exists, err
}

// parseTenantCSV reads and validates the CSV structure, returning the parsed
// rows and the uploaded filename. Column order is independent (spec §3.2a).
func parseTenantCSV(r *http.Request) ([]tenantImportRow, string, error) {
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		return nil, "", errors.New("invalid multipart form")
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		return nil, "", errors.New("CSV file is required")
	}
	defer file.Close()

	records, err := csv.NewReader(file).ReadAll()
	if err != nil {
		return nil, "", errors.New("invalid CSV file")
	}
	if len(records) < 2 {
		return nil, "", errors.New("CSV must have a header row and at least one data row")
	}

	index := make(map[string]int, len(records[0]))
	for i, name := range records[0] {
		index[strings.ToLower(strings.TrimSpace(name))] = i
	}
	for _, col := range tenantImportCSVColumns {
		if _, ok := index[col]; !ok {
			return nil, "", fmt.Errorf("CSV header must contain %s", strings.Join(tenantImportCSVColumns, ", "))
		}
	}

	rows := make([]tenantImportRow, 0, len(records)-1)
	for _, record := range records[1:] {
		baseRent, err := strconv.ParseFloat(strings.TrimSpace(record[index["base_rent"]]), 64)
		if err != nil || baseRent < 0 {
			return nil, "", errors.New("base_rent must be a non-negative number")
		}
		deposit, err := strconv.ParseFloat(strings.TrimSpace(record[index["deposit_amount"]]), 64)
		if err != nil || deposit < 0 {
			return nil, "", errors.New("deposit_amount must be a non-negative number")
		}

		rows = append(rows, tenantImportRow{
			UnitLabel:      strings.TrimSpace(record[index["unit_label"]]),
			UnitType:       strings.TrimSpace(record[index["unit_type"]]),
			BaseRent:       baseRent,
			DepositAmount:  deposit,
			TenantFullName: strings.TrimSpace(record[index["tenant_full_name"]]),
			TenantPhone:    strings.TrimSpace(record[index["tenant_phone"]]),
			TenantIDNumber: strings.TrimSpace(record[index["tenant_id_number"]]),
			LeaseStartDate: strings.TrimSpace(record[index["lease_start_date"]]),
			MonthlyRent:    strings.TrimSpace(record[index["monthly_rent"]]),
			RentDueDay:     strings.TrimSpace(record[index["rent_due_day"]]),
			LateFeePctDay:  strings.TrimSpace(record[index["late_fee_pct_per_day"]]),
		})
	}

	return rows, header.Filename, nil
}

// floatToCents converts a KES float to integer cents for the ledger.
func floatToCents(f float64) int64 {
	return int64(math.Round(f * 100))
}

// processTenantImportRow creates one unit (and, for a sitting tenant, its
// tenant + lease) in a per-row savepoint, then posts the deposit. Bad rows are
// skipped without rolling back previously-created rows (spec §3.2a).
func (s *Server) processTenantImportRow(ctx context.Context, tx pgx.Tx, organizationID, recordedBy, propertyID uuid.UUID, defaults struct {
	RentDueDay       int
	LateFeePctPerDay float64
}, row tenantImportRow) (tenantImportRowResult, error) {
	if err := validateUnitLabel(row.UnitLabel); err != nil {
		return tenantImportRowResult{}, err
	}

	hasTenant := row.TenantFullName != "" || row.TenantPhone != "" || row.LeaseStartDate != ""
	if hasTenant {
		if row.TenantFullName == "" || row.TenantPhone == "" || row.LeaseStartDate == "" {
			return tenantImportRowResult{}, errors.New("tenant_full_name, tenant_phone, and lease_start_date must be blank together or filled together")
		}
		if !msisdn.Valid(row.TenantPhone) {
			return tenantImportRowResult{}, errors.New("tenant_phone must be a valid Kenyan MSISDN")
		}
		if _, err := time.Parse("2006-01-02", row.LeaseStartDate); err != nil {
			return tenantImportRowResult{}, errors.New("lease_start_date must be YYYY-MM-DD")
		}
	}

	monthlyRent := row.BaseRent
	if row.MonthlyRent != "" {
		v, err := strconv.ParseFloat(row.MonthlyRent, 64)
		if err != nil || v < 0 {
			return tenantImportRowResult{}, errors.New("monthly_rent must be a non-negative number")
		}
		monthlyRent = v
	}
	rentDueDay := defaults.RentDueDay
	if row.RentDueDay != "" {
		v, err := strconv.Atoi(row.RentDueDay)
		if err != nil || v < 1 || v > 31 {
			return tenantImportRowResult{}, errors.New("rent_due_day must be between 1 and 31")
		}
		rentDueDay = v
	}
	lateFeePct := defaults.LateFeePctPerDay
	if row.LateFeePctDay != "" {
		v, err := strconv.ParseFloat(row.LateFeePctDay, 64)
		if err != nil || v < 0 {
			return tenantImportRowResult{}, errors.New("late_fee_pct_per_day must be a non-negative number")
		}
		lateFeePct = v
	}

	sp, err := tx.Begin(ctx)
	if err != nil {
		return tenantImportRowResult{}, err
	}

	unitStatus := "vacant"
	if hasTenant {
		unitStatus = "occupied"
	}
	var unitID uuid.UUID
	err = sp.QueryRow(ctx, `
		INSERT INTO units (property_id, unit_label, unit_type, base_rent, deposit_amount, status)
		VALUES ($1, $2, NULLIF($3, ''), $4, $5, $6::unit_status)
		RETURNING id`,
		propertyID, row.UnitLabel, row.UnitType, row.BaseRent, row.DepositAmount, unitStatus,
	).Scan(&unitID)
	if err != nil {
		sp.Rollback(ctx)
		if isUniqueViolation(err) {
			return tenantImportRowResult{}, ErrUnitLabelTaken
		}
		return tenantImportRowResult{}, fmt.Errorf("insert unit: %w", err)
	}

	if !hasTenant {
		if err := sp.Commit(ctx); err != nil {
			return tenantImportRowResult{}, err
		}
		return tenantImportRowResult{UnitLabel: row.UnitLabel, Status: "created"}, nil
	}

	var tenantID uuid.UUID
	err = sp.QueryRow(ctx, `
		INSERT INTO tenants (organization_id, full_name, phone, id_number)
		VALUES ($1, $2, $3, NULLIF($4, ''))
		RETURNING id`,
		organizationID, row.TenantFullName, row.TenantPhone, row.TenantIDNumber,
	).Scan(&tenantID)
	if err != nil {
		sp.Rollback(ctx)
		return tenantImportRowResult{}, fmt.Errorf("insert tenant: %w", err)
	}

	if _, err := sp.Exec(ctx, `
		INSERT INTO leases (unit_id, tenant_id, monthly_rent, deposit_paid, start_date, rent_due_day, late_fee_pct_per_day, status, created_by)
		VALUES ($1, $2, $3, $4, $5::date, $6, $7, 'active', $8)`,
		unitID, tenantID, monthlyRent, row.DepositAmount, row.LeaseStartDate, rentDueDay, lateFeePct, recordedBy,
	); err != nil {
		sp.Rollback(ctx)
		return tenantImportRowResult{}, fmt.Errorf("insert lease: %w", err)
	}

	if err := sp.Commit(ctx); err != nil {
		return tenantImportRowResult{}, err
	}

	if s.money != nil && row.DepositAmount > 0 {
		if _, err := s.money.ExecuteDepositTx(ctx, money.DepositTxInput{
			OrganizationID: organizationID,
			TenantID:       tenantID,
			UnitID:         unitID,
			PropertyID:     propertyID,
			Amount:         floatToCents(row.DepositAmount),
			Method:         "bank",
			Narrative:      "deposit paid at import",
			IdempotencyKey: fmt.Sprintf("tenant-import-deposit:%s", unitID),
			RecordedBy:     recordedBy,
		}); err != nil {
			s.logger.Error("post import deposit", "unit_id", unitID, "error", err)
		}
	}

	return tenantImportRowResult{UnitLabel: row.UnitLabel, Status: "created"}, nil
}

