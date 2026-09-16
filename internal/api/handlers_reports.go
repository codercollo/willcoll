package api

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/codercollo/willcoll-sys/internal/reports"
	"github.com/google/uuid"
	"github.com/julienschmidt/httprouter"
)

const (
	defaultPageSize = 20
	maxPageSize     = 100
)

// listArrears handles GET /v1/reports/arrears.
func (s *Server) listArrears(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
	claims, _ := claimsFromContext(r.Context())
	tx, ok := requestTxFromContext(r.Context())
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "the server encountered a problem and could not process your request")
		return
	}

	q := r.URL.Query()
	propertyID, ok := optionalUUIDQuery(w, q, "property_id")
	if !ok {
		return
	}
	page, ok := parsePageQuery(w, q)
	if !ok {
		return
	}
	sort, ok := parseSortQuery(w, q, "unit_label", "unit_label", "-unit_label", "arrears", "-arrears")
	if !ok {
		return
	}

	result, err := s.reports.ListArrears(r.Context(), tx, reports.ArrearsFilters{
		PropertyID: propertyID,
		Role:       claims.Role,
		UserID:     claims.UserID,
		Sort:       sort,
		Page:       page,
	})
	if err != nil {
		s.logger.Error("arrears report", "error", err)
		writeJSONError(w, http.StatusInternalServerError, "the server encountered a problem and could not process your request")
		return
	}

	writeJSON(w, http.StatusOK, result, "")
}

// listCollections handles GET /v1/reports/collections.
func (s *Server) listCollections(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
	claims, _ := claimsFromContext(r.Context())
	tx, ok := requestTxFromContext(r.Context())
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "the server encountered a problem and could not process your request")
		return
	}

	q := r.URL.Query()
	propertyID, ok := optionalUUIDQuery(w, q, "property_id")
	if !ok {
		return
	}
	month := strings.TrimSpace(q.Get("month"))
	if month == "" {
		writeJSONError(w, http.StatusBadRequest, "month is required (YYYY-MM)")
		return
	}
	period, err := time.Parse("2006-01", month)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "month must be YYYY-MM")
		return
	}
	page, ok := parsePageQuery(w, q)
	if !ok {
		return
	}
	sort, ok := parseSortQuery(w, q, "property_id", "property_id", "-property_id", "collected", "-collected")
	if !ok {
		return
	}

	result, err := s.reports.ListCollections(r.Context(), tx, reports.CollectionsFilters{
		PropertyID: propertyID,
		Month:      period,
		Role:       claims.Role,
		UserID:     claims.UserID,
		Sort:       sort,
		Page:       page,
	})
	if err != nil {
		s.logger.Error("collections report", "error", err)
		writeJSONError(w, http.StatusInternalServerError, "the server encountered a problem and could not process your request")
		return
	}

	writeJSON(w, http.StatusOK, result, "")
}

// portfolio handles GET /v1/reports/portfolio.
func (s *Server) portfolio(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
	claims, _ := claimsFromContext(r.Context())
	tx, ok := requestTxFromContext(r.Context())
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "the server encountered a problem and could not process your request")
		return
	}

	q := r.URL.Query()
	page, ok := parsePageQuery(w, q)
	if !ok {
		return
	}
	sort, ok := parseSortQuery(w, q, "name", "name", "-name", "units", "-units")
	if !ok {
		return
	}

	result, err := s.reports.ListPortfolio(r.Context(), tx, reports.PortfolioFilters{
		Search: strings.TrimSpace(q.Get("q")),
		Role:   claims.Role,
		UserID: claims.UserID,
		Sort:   sort,
		Page:   page,
	})
	if err != nil {
		s.logger.Error("portfolio report", "error", err)
		writeJSONError(w, http.StatusInternalServerError, "the server encountered a problem and could not process your request")
		return
	}

	writeJSON(w, http.StatusOK, result, "")
}

// unitStatement handles GET /v1/units/:id/statement.
func (s *Server) unitStatement(w http.ResponseWriter, r *http.Request, ps httprouter.Params) {
	unitID, err := uuid.Parse(ps.ByName("id"))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid unit id")
		return
	}
	claims, _ := claimsFromContext(r.Context())
	tx, ok := requestTxFromContext(r.Context())
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "the server encountered a problem and could not process your request")
		return
	}

	page, ok := parsePageQuery(w, r.URL.Query())
	if !ok {
		return
	}

	result, err := s.reports.ListUnitStatement(r.Context(), tx, reports.UnitStatementFilters{
		UnitID: unitID,
		Role:   claims.Role,
		UserID: claims.UserID,
		Page:   page,
	})
	if err != nil {
		s.logger.Error("unit statement report", "error", err)
		writeJSONError(w, http.StatusInternalServerError, "the server encountered a problem and could not process your request")
		return
	}

	writeJSON(w, http.StatusOK, result, "")
}

func optionalUUIDQuery(w http.ResponseWriter, q url.Values, key string) (*uuid.UUID, bool) {
	raw := strings.TrimSpace(q.Get(key))
	if raw == "" {
		return nil, true
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid "+key)
		return nil, false
	}
	return &id, true
}

func parsePageQuery(w http.ResponseWriter, q url.Values) (reports.Page, bool) {
	page, ok := parsePositiveIntQuery(w, q, "page", 1)
	if !ok {
		return reports.Page{}, false
	}
	pageSize, ok := parsePositiveIntQuery(w, q, "page_size", defaultPageSize)
	if !ok {
		return reports.Page{}, false
	}
	if pageSize > maxPageSize {
		writeJSONError(w, http.StatusBadRequest, "page_size must be 100 or fewer")
		return reports.Page{}, false
	}
	return reports.Page{Number: page, Size: pageSize}, true
}

func parsePositiveIntQuery(w http.ResponseWriter, q url.Values, key string, fallback int) (int, bool) {
	raw := strings.TrimSpace(q.Get(key))
	if raw == "" {
		return fallback, true
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 1 {
		writeJSONError(w, http.StatusBadRequest, key+" must be a positive integer")
		return 0, false
	}
	return value, true
}

func parseSortQuery(w http.ResponseWriter, q url.Values, fallback string, allowed ...string) (string, bool) {
	sort := strings.TrimSpace(q.Get("sort"))
	if sort == "" {
		return fallback, true
	}
	for _, field := range allowed {
		if sort == field {
			return sort, true
		}
	}
	writeJSONError(w, http.StatusBadRequest, "unsupported sort")
	return "", false
}
