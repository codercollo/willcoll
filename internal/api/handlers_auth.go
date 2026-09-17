package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/julienschmidt/httprouter"
)

type inviteUserRequest struct {
	FullName string `json:"full_name"`
	Phone    string `json:"phone"`
	Email    string `json:"email"`
}

type activateUserRequest struct {
	Token    string `json:"token"`
	Password string `json:"password"`
}

type setPasswordRequest struct {
	Token       string `json:"token"`
	NewPassword string `json:"new_password"`
}

// inviteAgent handles POST /v1/agents.
func (s *Server) inviteAgent(w http.ResponseWriter, r *http.Request, ps httprouter.Params) {
	s.inviteUser(w, r, ps, "agent")
}

// inviteManager handles POST /v1/organization/managers — a Manager inviting a
// SECOND Manager into their own Organization. Same activation-link mechanism
// as an Agent invite (spec §3.1a, §6.1), just role='manager'; unlike the
// Organization's first Manager (registerManager), an invited Manager is not
// trusted until they activate.
func (s *Server) inviteManager(w http.ResponseWriter, r *http.Request, ps httprouter.Params) {
	s.inviteUser(w, r, ps, "manager")
}

// inviteUser creates an invited (activated=false, status='invited') user of
// role in the caller's own organization_id and emails them an activation
// link (scope='activation', ch.15.2 pattern) — shared by the Agent and
// second-Manager invite flows, which differ only in the role granted.
func (s *Server) inviteUser(w http.ResponseWriter, r *http.Request, _ httprouter.Params, role string) {
	var input inviteUserRequest
	if !readJSON(w, r, &input) {
		return
	}

	if strings.TrimSpace(input.FullName) == "" ||
		strings.TrimSpace(input.Phone) == "" ||
		strings.TrimSpace(input.Email) == "" {
		writeJSONError(w, http.StatusBadRequest, "full_name, phone, and email are required")
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

	var invitedID uuid.UUID
	err := tx.QueryRow(r.Context(), `
		INSERT INTO users (id, organization_id, full_name, phone, email, password_hash, role, is_super_manager, activated, status, invited_by)
		VALUES (gen_random_uuid(), $1, $2, $3, $4, NULL, $5, false, false, 'invited', $6)
		RETURNING id`,
		claims.OrganizationID, input.FullName, input.Phone, input.Email, role, claims.UserID,
	).Scan(&invitedID)
	if err != nil {
		if isUniqueViolation(err) {
			writeJSONError(w, http.StatusConflict, "an account with that email or phone already exists")
			return
		}
		s.logger.Error("invite user", "error", err, "role", role)
		writeJSONError(w, http.StatusInternalServerError, "the server encountered a problem and could not process your request")
		return
	}

	activationToken, err := s.auth.GenerateActivationToken()
	if err != nil {
		s.logger.Error("generate activation token", "error", err)
		writeJSONError(w, http.StatusInternalServerError, "the server encountered a problem and could not process your request")
		return
	}

	if _, err := tx.Exec(r.Context(), `
		INSERT INTO tokens (hash, user_id, organization_id, expiry, scope)
		VALUES ($1, $2, $3, $4, 'activation')`,
		activationToken.Hash, invitedID, claims.OrganizationID, activationToken.Expiry,
	); err != nil {
		s.logger.Error("store activation token", "error", err)
		writeJSONError(w, http.StatusInternalServerError, "the server encountered a problem and could not process your request")
		return
	}

	brandVars, err := s.branding.BrandVars(r.Context(), claims.OrganizationID)
	if err != nil {
		s.logger.Error("load brand vars", "error", err)
		writeJSONError(w, http.StatusInternalServerError, "the server encountered a problem and could not process your request")
		return
	}
	var emailFromName *string
	if err := tx.QueryRow(r.Context(), `
		SELECT email_from_name
		FROM organizations
		WHERE id = $1`,
		claims.OrganizationID,
	).Scan(&emailFromName); err != nil {
		s.logger.Error("load email from name", "error", err)
		writeJSONError(w, http.StatusInternalServerError, "the server encountered a problem and could not process your request")
		return
	}
	fromName := ""
	if emailFromName != nil {
		fromName = *emailFromName
	} else {
		fromName = brandVars.BrandName + " via Willcoll"
	}

	if err := s.mailer.Send(r.Context(), input.Email, "activation-password.tmpl", map[string]string{
		"Token":         activationToken.Plaintext,
		"BrandName":     brandVars.BrandName,
		"LogoURL":       brandVars.LogoURL,
		"EmailFromName": fromName,
	}); err != nil {
		s.logger.Error("send activation email", "error", err)
		writeJSONError(w, http.StatusInternalServerError, "the server encountered a problem and could not process your request")
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{"id": invitedID}, "data")
}

// activateUser handles PUT /v1/users/activate.
func (s *Server) activateUser(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
	var input activateUserRequest
	if !readJSON(w, r, &input) {
		return
	}

	if strings.TrimSpace(input.Token) == "" || input.Password == "" {
		writeJSONError(w, http.StatusBadRequest, "token and password are required")
		return
	}

	tokenHash := s.auth.HashToken(input.Token)

	tx, err := s.pool.Begin(r.Context())
	if err != nil {
		s.logger.Error("begin activation transaction", "error", err)
		writeJSONError(w, http.StatusInternalServerError, "the server encountered a problem and could not process your request")
		return
	}
	defer func() { _ = tx.Rollback(r.Context()) }()

	var userID uuid.UUID
	err = tx.QueryRow(r.Context(), `
		DELETE FROM tokens
		WHERE hash = $1 AND scope = 'activation' AND expiry > now()
		RETURNING user_id`,
		tokenHash,
	).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		writeJSONError(w, http.StatusBadRequest, "invalid or expired activation token")
		return
	}
	if err != nil {
		s.logger.Error("consume activation token", "error", err)
		writeJSONError(w, http.StatusInternalServerError, "the server encountered a problem and could not process your request")
		return
	}

	passwordHash, err := s.auth.HashPassword(input.Password)
	if err != nil {
		s.logger.Error("hash password", "error", err)
		writeJSONError(w, http.StatusInternalServerError, "the server encountered a problem and could not process your request")
		return
	}

	if _, err := tx.Exec(r.Context(), `
		UPDATE users
		SET activated = true, status = 'active', password_hash = $2, updated_at = now()
		WHERE id = $1`,
		userID, passwordHash,
	); err != nil {
		s.logger.Error("activate user", "error", err)
		writeJSONError(w, http.StatusInternalServerError, "the server encountered a problem and could not process your request")
		return
	}

	if err := tx.Commit(r.Context()); err != nil {
		s.logger.Error("commit activation", "error", err)
		writeJSONError(w, http.StatusInternalServerError, "the server encountered a problem and could not process your request")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "active"}, "data")
}

// resetAgentPassword handles POST /v1/agents/:id/reset-password.
func (s *Server) resetAgentPassword(w http.ResponseWriter, r *http.Request, ps httprouter.Params) {
	agentID, err := uuid.Parse(ps.ByName("id"))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid agent id")
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

	var email string
	err = tx.QueryRow(r.Context(), `
		SELECT email
		FROM users
		WHERE id = $1 AND role = 'agent' AND organization_id = $2`,
		agentID, claims.OrganizationID,
	).Scan(&email)
	if errors.Is(err, pgx.ErrNoRows) {
		writeJSONError(w, http.StatusNotFound, "agent not found")
		return
	}
	if err != nil {
		s.logger.Error("lookup agent", "error", err)
		writeJSONError(w, http.StatusInternalServerError, "the server encountered a problem and could not process your request")
		return
	}

	if _, err := tx.Exec(r.Context(), `
		DELETE FROM tokens
		WHERE user_id = $1 AND scope = 'password-reset'`,
		agentID,
	); err != nil {
		s.logger.Error("invalidate prior reset tokens", "error", err)
		writeJSONError(w, http.StatusInternalServerError, "the server encountered a problem and could not process your request")
		return
	}

	resetToken, err := s.auth.GeneratePasswordResetToken()
	if err != nil {
		s.logger.Error("generate password reset token", "error", err)
		writeJSONError(w, http.StatusInternalServerError, "the server encountered a problem and could not process your request")
		return
	}

	if _, err := tx.Exec(r.Context(), `
		INSERT INTO tokens (hash, user_id, organization_id, expiry, scope)
		VALUES ($1, $2, $3, $4, 'password-reset')`,
		resetToken.Hash, agentID, claims.OrganizationID, resetToken.Expiry,
	); err != nil {
		s.logger.Error("store password reset token", "error", err)
		writeJSONError(w, http.StatusInternalServerError, "the server encountered a problem and could not process your request")
		return
	}

	brandVars, err := s.branding.BrandVars(r.Context(), claims.OrganizationID)
	if err != nil {
		s.logger.Error("load brand vars", "error", err)
		writeJSONError(w, http.StatusInternalServerError, "the server encountered a problem and could not process your request")
		return
	}
	var emailFromName *string
	if err := tx.QueryRow(r.Context(), `
		SELECT email_from_name
		FROM organizations
		WHERE id = $1`,
		claims.OrganizationID,
	).Scan(&emailFromName); err != nil {
		s.logger.Error("load email from name", "error", err)
		writeJSONError(w, http.StatusInternalServerError, "the server encountered a problem and could not process your request")
		return
	}
	fromName := ""
	if emailFromName != nil {
		fromName = *emailFromName
	} else {
		fromName = brandVars.BrandName + " via Willcoll"
	}

	if err := s.mailer.Send(r.Context(), email, "reset-password.tmpl", map[string]string{
		"Token":         resetToken.Plaintext,
		"BrandName":     brandVars.BrandName,
		"LogoURL":       brandVars.LogoURL,
		"EmailFromName": fromName,
	}); err != nil {
		s.logger.Error("send password reset email", "error", err)
		writeJSONError(w, http.StatusInternalServerError, "the server encountered a problem and could not process your request")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "reset email sent"}, "data")
}

// setPassword handles PUT /v1/users/password.
func (s *Server) setPassword(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
	var input setPasswordRequest
	if !readJSON(w, r, &input) {
		return
	}

	if strings.TrimSpace(input.Token) == "" || input.NewPassword == "" {
		writeJSONError(w, http.StatusBadRequest, "token and new_password are required")
		return
	}

	tokenHash := s.auth.HashToken(input.Token)

	tx, err := s.pool.Begin(r.Context())
	if err != nil {
		s.logger.Error("begin password reset transaction", "error", err)
		writeJSONError(w, http.StatusInternalServerError, "the server encountered a problem and could not process your request")
		return
	}
	defer func() { _ = tx.Rollback(r.Context()) }()

	var userID uuid.UUID
	err = tx.QueryRow(r.Context(), `
		DELETE FROM tokens
		WHERE hash = $1 AND scope = 'password-reset' AND expiry > now()
		RETURNING user_id`,
		tokenHash,
	).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		writeJSONError(w, http.StatusBadRequest, "invalid or expired password reset token")
		return
	}
	if err != nil {
		s.logger.Error("consume password reset token", "error", err)
		writeJSONError(w, http.StatusInternalServerError, "the server encountered a problem and could not process your request")
		return
	}

	passwordHash, err := s.auth.HashPassword(input.NewPassword)
	if err != nil {
		s.logger.Error("hash password", "error", err)
		writeJSONError(w, http.StatusInternalServerError, "the server encountered a problem and could not process your request")
		return
	}

	if _, err := tx.Exec(r.Context(), `
		UPDATE users
		SET password_hash = $2, updated_at = now()
		WHERE id = $1`,
		userID, passwordHash,
	); err != nil {
		s.logger.Error("update password", "error", err)
		writeJSONError(w, http.StatusInternalServerError, "the server encountered a problem and could not process your request")
		return
	}

	if _, err := tx.Exec(r.Context(), `
		UPDATE sessions
		SET revoked_at = now()
		WHERE user_id = $1`,
		userID,
	); err != nil {
		s.logger.Error("revoke sessions", "error", err)
		writeJSONError(w, http.StatusInternalServerError, "the server encountered a problem and could not process your request")
		return
	}

	if err := tx.Commit(r.Context()); err != nil {
		s.logger.Error("commit password reset", "error", err)
		writeJSONError(w, http.StatusInternalServerError, "the server encountered a problem and could not process your request")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "password updated"}, "data")
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
