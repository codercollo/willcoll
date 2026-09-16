package api

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strings"

	"github.com/codercollo/willcoll-sys/internal/branding"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/julienschmidt/httprouter"
)

var slugPattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

type updateOrganizationRequest struct {
	Name *string `json:"name"`
	Slug *string `json:"slug"`
}

type organizationProfile struct {
	Name          string  `json:"name"`
	Slug          string  `json:"slug"`
	BrandName     string  `json:"brand_name"`
	LogoURL       *string `json:"logo_url"`
	AccentKey     string  `json:"accent_key"`
	SMSSenderID   *string `json:"sms_sender_id"`
	EmailFromName *string `json:"email_from_name"`
}

// requireSuperManager confirms the caller is both a Manager and the
// Organization's super-manager (spec §2.1).
func (s *Server) requireSuperManager(w http.ResponseWriter, r *http.Request) bool {
	claims, ok := claimsFromContext(r.Context())
	if !ok {
		writeJSONError(w, http.StatusUnauthorized, "invalid or missing authentication token")
		return false
	}

	tx, ok := requestTxFromContext(r.Context())
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "the server encountered a problem and could not process your request")
		return false
	}

	var isSuper bool
	err := tx.QueryRow(r.Context(), `
		SELECT is_super_manager
		FROM users
		WHERE id = $1`,
		claims.UserID,
	).Scan(&isSuper)
	if err != nil {
		s.logger.Error("lookup super manager", "error", err)
		writeJSONError(w, http.StatusInternalServerError, "the server encountered a problem and could not process your request")
		return false
	}
	if !isSuper {
		writeJSONError(w, http.StatusForbidden, "super manager permission required")
		return false
	}
	return true
}

func (s *Server) getOrganizationProfile(ctx context.Context, orgID uuid.UUID) (organizationProfile, error) {
	tx, ok := requestTxFromContext(ctx)
	if !ok {
		return organizationProfile{}, errors.New("request transaction missing")
	}

	var p organizationProfile
	err := tx.QueryRow(ctx, `
		SELECT name, slug, brand_name, logo_url, accent_key, sms_sender_id, email_from_name
		FROM organizations
		WHERE id = $1`,
		orgID,
	).Scan(&p.Name, &p.Slug, &p.BrandName, &p.LogoURL, &p.AccentKey, &p.SMSSenderID, &p.EmailFromName)
	return p, err
}

func validateSlug(slug string) error {
	if slug == "" {
		return errors.New("slug is required")
	}
	if len(slug) > 100 {
		return errors.New("slug must be 100 characters or fewer")
	}
	if !slugPattern.MatchString(slug) {
		return errors.New("slug must use lowercase letters, numbers, and hyphens only")
	}
	return nil
}

// publicBranding handles GET /v1/public/branding.
func (s *Server) publicBranding(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
	slug := strings.TrimSpace(r.URL.Query().Get("slug"))
	if slug == "" {
		writeJSONError(w, http.StatusBadRequest, "slug is required")
		return
	}

	b, err := s.branding.GetBrandingBySlug(r.Context(), slug)
	if errors.Is(err, pgx.ErrNoRows) {
		writeJSONError(w, http.StatusNotFound, "branding not found")
		return
	}
	if err != nil {
		s.logger.Error("get public branding", "error", err)
		writeJSONError(w, http.StatusInternalServerError, "the server encountered a problem and could not process your request")
		return
	}

	writeJSON(w, http.StatusOK, b, "data")
}

// getOrganization handles GET /v1/organization.
func (s *Server) getOrganization(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
	claims, ok := claimsFromContext(r.Context())
	if !ok {
		writeJSONError(w, http.StatusUnauthorized, "invalid or missing authentication token")
		return
	}

	p, err := s.getOrganizationProfile(r.Context(), claims.OrganizationID)
	if errors.Is(err, pgx.ErrNoRows) {
		writeJSONError(w, http.StatusNotFound, "organization not found")
		return
	}
	if err != nil {
		s.logger.Error("get organization", "error", err)
		writeJSONError(w, http.StatusInternalServerError, "the server encountered a problem and could not process your request")
		return
	}

	writeJSON(w, http.StatusOK, p, "data")
}

// patchOrganization handles PATCH /v1/organization.
func (s *Server) patchOrganization(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
	if !s.requireSuperManager(w, r) {
		return
	}

	var input updateOrganizationRequest
	if !readJSON(w, r, &input) {
		return
	}
	if input.Name == nil && input.Slug == nil {
		writeJSONError(w, http.StatusBadRequest, "name or slug is required")
		return
	}

	claims, _ := claimsFromContext(r.Context())
	tx, ok := requestTxFromContext(r.Context())
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "the server encountered a problem and could not process your request")
		return
	}

	var current struct {
		Name string
		Slug string
	}
	err := tx.QueryRow(r.Context(), `
		SELECT name, slug
		FROM organizations
		WHERE id = $1`,
		claims.OrganizationID,
	).Scan(&current.Name, &current.Slug)
	if errors.Is(err, pgx.ErrNoRows) {
		writeJSONError(w, http.StatusNotFound, "organization not found")
		return
	}
	if err != nil {
		s.logger.Error("lookup organization", "error", err)
		writeJSONError(w, http.StatusInternalServerError, "the server encountered a problem and could not process your request")
		return
	}

	name := current.Name
	if input.Name != nil {
		name = strings.TrimSpace(*input.Name)
		if name == "" {
			writeJSONError(w, http.StatusBadRequest, "name is required")
			return
		}
	}

	slug := current.Slug
	if input.Slug != nil {
		slug = strings.TrimSpace(*input.Slug)
		if err := validateSlug(slug); err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
	}

	if _, err := tx.Exec(r.Context(), `
		UPDATE organizations
		SET name = $2, slug = $3, updated_at = now()
		WHERE id = $1`,
		claims.OrganizationID, name, slug,
	); err != nil {
		if isUniqueViolation(err) {
			writeJSONError(w, http.StatusConflict, "slug is already in use")
			return
		}
		s.logger.Error("update organization", "error", err)
		writeJSONError(w, http.StatusInternalServerError, "the server encountered a problem and could not process your request")
		return
	}

	p, err := s.getOrganizationProfile(r.Context(), claims.OrganizationID)
	if err != nil {
		s.logger.Error("reload organization", "error", err)
		writeJSONError(w, http.StatusInternalServerError, "the server encountered a problem and could not process your request")
		return
	}

	writeJSON(w, http.StatusOK, p, "data")
}

// patchOrganizationBranding handles PATCH /v1/organization/branding.
func (s *Server) patchOrganizationBranding(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
	if !s.requireSuperManager(w, r) {
		return
	}

	claims, _ := claimsFromContext(r.Context())

	if err := r.ParseMultipartForm(1 << 20); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid multipart form")
		return
	}

	input := branding.UpdateBrandingInput{
		BrandName:     r.FormValue("brand_name"),
		AccentKey:     r.FormValue("accent_key"),
		SMSSenderID:   r.FormValue("sms_sender_id"),
		EmailFromName: r.FormValue("email_from_name"),
	}

	updated, err := s.branding.UpdateBranding(r.Context(), claims.OrganizationID, input)
	if err != nil {
		switch {
		case errors.Is(err, branding.ErrAccentKeyInvalid),
			errors.Is(err, branding.ErrBrandNameRequired),
			errors.Is(err, branding.ErrBrandNameTooLong),
			errors.Is(err, branding.ErrBrandNameInvalid):
			writeJSONError(w, http.StatusUnprocessableEntity, err.Error())
		default:
			s.logger.Error("update branding", "error", err)
			writeJSONError(w, http.StatusInternalServerError, "the server encountered a problem and could not process your request")
		}
		return
	}

	file, header, err := r.FormFile("logo")
	if err == nil {
		defer file.Close()
		if _, err := s.branding.UploadLogo(r.Context(), claims.OrganizationID, file, header.Header.Get("Content-Type")); err != nil {
			s.logger.Error("upload logo", "error", err)
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		updated, err = s.branding.GetBranding(r.Context(), claims.OrganizationID)
		if err != nil {
			s.logger.Error("reload branding", "error", err)
			writeJSONError(w, http.StatusInternalServerError, "the server encountered a problem and could not process your request")
			return
		}
	} else if !errors.Is(err, http.ErrMissingFile) {
		writeJSONError(w, http.StatusBadRequest, "invalid logo upload")
		return
	}

	writeJSON(w, http.StatusOK, updated, "data")
}
