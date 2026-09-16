package api

import (
	"errors"
	"net/http"

	"github.com/codercollo/willcoll-sys/internal/tenancy"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/julienschmidt/httprouter"
)

type registerManagerRequest struct {
	Name     string `json:"name"`
	FullName string `json:"full_name"`
	Phone    string `json:"phone"`
	Email    string `json:"email"`
	Password string `json:"password"`
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// registerManager handles POST /v1/auth/register-manager.
func (s *Server) registerManager(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
	var input registerManagerRequest
	if !readJSON(w, r, &input) {
		return
	}

	passwordHash, err := s.auth.HashPassword(input.Password)
	if err != nil {
		s.logger.Error("hash password", "error", err)
		writeJSONError(w, http.StatusInternalServerError, "the server encountered a problem and could not process your request")
		return
	}

	org, user, err := s.tenancy.CreateOrganizationWithFirstManagerTx(r.Context(), tenancy.CreateOrganizationInput{
		Name:         input.Name,
		FullName:     input.FullName,
		Phone:        input.Phone,
		Email:        input.Email,
		PasswordHash: passwordHash,
	})
	if err != nil {
		switch {
		case errors.Is(err, tenancy.ErrOrganizationNameRequired),
			errors.Is(err, tenancy.ErrManagerNameRequired),
			errors.Is(err, tenancy.ErrManagerPhoneRequired),
			errors.Is(err, tenancy.ErrManagerEmailRequired),
			errors.Is(err, tenancy.ErrManagerPasswordRequired):
			writeJSONError(w, http.StatusBadRequest, err.Error())
		default:
			s.logger.Error("register manager", "error", err)
			writeJSONError(w, http.StatusInternalServerError, "the server encountered a problem and could not process your request")
		}
		return
	}

	token, err := s.auth.IssueToken(user.ID, user.Role, org.ID)
	if err != nil {
		s.logger.Error("issue token", "error", err)
		writeJSONError(w, http.StatusInternalServerError, "the server encountered a problem and could not process your request")
		return
	}

	writeJSON(w, http.StatusCreated, map[string]string{"token": token}, "data")
}

// login handles POST /v1/auth/login.
func (s *Server) login(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
	var input loginRequest
	if !readJSON(w, r, &input) {
		return
	}

	var (
		userID         uuid.UUID
		organizationID uuid.UUID
		role           string
		passwordHash   *string
	)
	err := s.pool.QueryRow(r.Context(), `
		SELECT id, organization_id, role::text, password_hash
		FROM users
		WHERE email = $1`,
		input.Email,
	).Scan(&userID, &organizationID, &role, &passwordHash)
	if errors.Is(err, pgx.ErrNoRows) {
		writeJSONError(w, http.StatusUnauthorized, "invalid email or password")
		return
	}
	if err != nil {
		s.logger.Error("lookup user", "error", err)
		writeJSONError(w, http.StatusInternalServerError, "the server encountered a problem and could not process your request")
		return
	}

	if passwordHash == nil || s.auth.ComparePassword(*passwordHash, input.Password) != nil {
		writeJSONError(w, http.StatusUnauthorized, "invalid email or password")
		return
	}

	token, err := s.auth.IssueToken(userID, role, organizationID)
	if err != nil {
		s.logger.Error("issue token", "error", err)
		writeJSONError(w, http.StatusInternalServerError, "the server encountered a problem and could not process your request")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"token": token}, "data")
}

// refresh handles POST /v1/auth/refresh.
func (s *Server) refresh(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
	token, ok := bearerToken(r)
	if !ok {
		writeJSONError(w, http.StatusUnauthorized, "invalid or missing authentication token")
		return
	}

	claims, err := s.auth.VerifyToken(token)
	if err != nil {
		writeJSONError(w, http.StatusUnauthorized, "invalid or missing authentication token")
		return
	}

	newToken, err := s.auth.IssueToken(claims.UserID, claims.Role, claims.OrganizationID)
	if err != nil {
		s.logger.Error("issue refreshed token", "error", err)
		writeJSONError(w, http.StatusInternalServerError, "the server encountered a problem and could not process your request")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"token": newToken}, "data")
}
