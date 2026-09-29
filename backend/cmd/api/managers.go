package main

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/codercollo/willcoll/backend/internal/data"
	"github.com/codercollo/willcoll/backend/internal/validator"
	"github.com/gin-gonic/gin"
)

// activationTokenTTL is how long an emailed activation code stays valid.
const activationTokenTTL = 3 * 24 * time.Hour

// registerManagerHandler handles POST /v1/managers: a management firm signs
// up (system-design.txt 1.2, 4.4). The account starts 'pending' and the
// welcome email, which carries the activation link and code, is sent in the
// background so the response doesn't wait on SMTP (Greenlight ch.13.4).
func (app *application) registerManagerHandler(c *gin.Context) {
	var input struct {
		FirmName string `json:"firm_name" validate:"required,max=500"`
		Username string `json:"username" validate:"required,max=100"`
		Email    string `json:"email" validate:"required,email"`
		Phone    string `json:"phone" validate:"required,phone"`
		Password string `json:"password" validate:"required"`
	}

	if err := app.readJSON(c, &input); err != nil {
		app.badRequestResponse(c, err)
		return
	}

	// Validate before hashing: bcrypt is deliberately slow, and it rejects
	// passwords over 72 bytes, which this check catches first.
	v := validator.New()
	v.Struct(input)
	data.ValidateManagerPasswordPlaintext(v, input.Password)
	if !v.Valid() {
		app.failedValidationResponse(c, v.Errors)
		return
	}

	manager := &data.Manager{
		FirmName: input.FirmName,
		Username: input.Username,
		Email:    input.Email,
		Phone:    input.Phone,
		Status:   data.ManagerStatusPending,
	}

	if err := manager.Password.Set(input.Password); err != nil {
		app.serverErrorResponse(c, err)
		return
	}

	if data.ValidateManager(v, manager); !v.Valid() {
		app.failedValidationResponse(c, v.Errors)
		return
	}

	err := app.models.Managers.Insert(c.Request.Context(), manager)
	if err != nil {
		switch {
		case errors.Is(err, data.ErrDuplicateEmail):
			app.failedValidationResponse(c, map[string]string{"email": "a manager with this email address already exists"})
		case errors.Is(err, data.ErrDuplicateUsername):
			app.failedValidationResponse(c, map[string]string{"username": "this username is already taken"})
		default:
			app.serverErrorResponse(c, err)
		}
		return
	}

	token, err := app.models.Tokens.New(c.Request.Context(), manager.ID, activationTokenTTL, data.ScopeActivation)
	if err != nil {
		app.serverErrorResponse(c, err)
		return
	}

	app.sendActivationEmail(manager, token)

	if err := app.writeJSON(c, http.StatusAccepted, envelope{"manager": manager}, nil); err != nil {
		app.serverErrorResponse(c, err)
	}
}

// activateManagerHandler handles PUT /v1/managers/activated: {"token": ...}
// from the welcome email. A valid, unexpired activation token flips the
// manager from 'pending' to 'active', stamps activated_at, and is then
// deleted with every other activation token for that manager, so it can't
// be replayed (Greenlight ch.14.4).
func (app *application) activateManagerHandler(c *gin.Context) {
	var input struct {
		Token string `json:"token"`
	}

	if err := app.readJSON(c, &input); err != nil {
		app.badRequestResponse(c, err)
		return
	}

	v := validator.New()
	if data.ValidateTokenPlaintext(v, input.Token); !v.Valid() {
		app.failedValidationResponse(c, v.Errors)
		return
	}

	ctx := c.Request.Context()

	manager, err := app.models.Managers.GetForToken(ctx, data.ScopeActivation, input.Token)
	if err != nil {
		switch {
		case errors.Is(err, data.ErrRecordNotFound):
			app.failedValidationResponse(c, map[string]string{"token": "invalid or expired activation token"})
		default:
			app.serverErrorResponse(c, err)
		}
		return
	}

	// Activation never lifts a suspension.
	if manager.Status == data.ManagerStatusSuspended {
		app.accountSuspendedResponse(c)
		return
	}

	now := time.Now()
	manager.Status = data.ManagerStatusActive
	manager.ActivatedAt = &now
	if manager.TrialEndsAt == nil { // the free trial starts once, at first activation
		end := app.trialEndsAt(now)
		manager.TrialEndsAt = &end
	}

	if err := app.models.Managers.Update(ctx, manager); err != nil {
		switch {
		case errors.Is(err, data.ErrEditConflict):
			app.editConflictResponse(c)
		default:
			app.serverErrorResponse(c, err)
		}
		return
	}

	if err := app.models.Tokens.DeleteAllForManager(ctx, data.ScopeActivation, manager.ID); err != nil {
		app.serverErrorResponse(c, err)
		return
	}

	if err := app.writeJSON(c, http.StatusOK, envelope{"manager": manager}, nil); err != nil {
		app.serverErrorResponse(c, err)
	}
}

// updateManagerPasswordHandler handles PUT /v1/managers/password:
// {"token": ..., "password": ...} from the reset email. A valid, unexpired
// password-reset token sets the new password (bcrypt, cost 12), then every
// token the manager holds is deleted and every session they have is
// destroyed, so a reset signs them out on all devices: if the old password
// leaked, whoever had it loses access too.
func (app *application) updateManagerPasswordHandler(c *gin.Context) {
	var input struct {
		Password string `json:"password" validate:"required"`
		Token    string `json:"token"`
	}

	if err := app.readJSON(c, &input); err != nil {
		app.badRequestResponse(c, err)
		return
	}

	v := validator.New()
	v.Struct(input)
	data.ValidateManagerPasswordPlaintext(v, input.Password)
	data.ValidateTokenPlaintext(v, input.Token)
	if !v.Valid() {
		app.failedValidationResponse(c, v.Errors)
		return
	}

	ctx := c.Request.Context()

	manager, err := app.models.Managers.GetForToken(ctx, data.ScopePasswordReset, input.Token)
	if err != nil {
		switch {
		case errors.Is(err, data.ErrRecordNotFound):
			app.failedValidationResponse(c, map[string]string{"token": "invalid or expired password reset token"})
		default:
			app.serverErrorResponse(c, err)
		}
		return
	}

	if manager.Status == data.ManagerStatusSuspended {
		app.accountSuspendedResponse(c)
		return
	}

	if err := manager.Password.Set(input.Password); err != nil {
		app.serverErrorResponse(c, err)
		return
	}

	if err := app.models.Managers.Update(ctx, manager); err != nil {
		switch {
		case errors.Is(err, data.ErrEditConflict):
			app.editConflictResponse(c)
		default:
			app.serverErrorResponse(c, err)
		}
		return
	}

	if err := app.models.Tokens.DeleteEveryScopeForManager(ctx, manager.ID); err != nil {
		app.serverErrorResponse(c, err)
		return
	}

	if _, err := app.revokeManagerSessions(ctx, manager.ID); err != nil {
		app.serverErrorResponse(c, err)
		return
	}

	// Shown with the next response, e.g. on the login page.
	app.putFlash(c, "Your password has been reset. Sign in with your new password.")

	if err := app.writeJSON(c, http.StatusOK, envelope{"message": "your password was successfully reset"}, nil); err != nil {
		app.serverErrorResponse(c, err)
	}
}

// sendActivationEmail sends the welcome + activation email in the
// background, tracked by app.wg so a graceful shutdown waits for it
// (Greenlight ch.13.4-13.5). A failed send is logged; the manager can ask
// for a fresh code through POST /v1/tokens/activation.
func (app *application) sendActivationEmail(manager *data.Manager, token *data.Token) {
	emailData := map[string]any{
		"FirmName":        manager.FirmName,
		"Username":        manager.Username,
		"ActivationURL":   app.config.frontendURL + "/auth/activate?token=" + token.Plaintext,
		"ActivationToken": token.Plaintext,
		"ExpiresIn":       humanDuration(activationTokenTTL),
	}

	app.background(func() {
		if err := app.mailer.Send(manager.Email, "manager_welcome.tmpl", emailData); err != nil {
			app.logger.Error("sending activation email", "manager_id", manager.ID, "error", err)
		}
	})
}

// humanDuration renders a token lifetime for an email: "3 days", "45 minutes".
func humanDuration(d time.Duration) string {
	plural := func(n int, unit string) string {
		if n == 1 {
			return fmt.Sprintf("1 %s", unit)
		}
		return fmt.Sprintf("%d %ss", n, unit)
	}

	switch {
	case d >= 24*time.Hour && d%(24*time.Hour) == 0:
		return plural(int(d/(24*time.Hour)), "day")
	case d >= time.Hour && d%time.Hour == 0:
		return plural(int(d/time.Hour), "hour")
	default:
		return plural(int(d/time.Minute), "minute")
	}
}
