package main

import (
	"errors"
	"net/http"
	"time"

	"github.com/codercollo/willcoll/backend/internal/data"
	"github.com/codercollo/willcoll/backend/internal/validator"
	"github.com/gin-gonic/gin"
)

// createActivationTokenHandler handles POST /v1/tokens/activation:
// {"email": ...} re-sends the welcome + activation email with a fresh code
// (Greenlight ch.14.3). Any earlier activation codes are revoked, so only
// the newest works.
//
// The response is the same 202 whether or not the email belongs to a
// pending account, so this endpoint can't be used to discover which email
// addresses are registered. (Greenlight answers 422 for unknown or
// already-activated emails; Willcoll deliberately doesn't.)
func (app *application) createActivationTokenHandler(c *gin.Context) {
	var input struct {
		Email string `json:"email" validate:"required,email"`
	}

	if err := app.readJSON(c, &input); err != nil {
		app.badRequestResponse(c, err)
		return
	}

	v := validator.New()
	if v.Struct(input); !v.Valid() {
		app.failedValidationResponse(c, v.Errors)
		return
	}

	ctx := c.Request.Context()
	accepted := envelope{"message": "if an unactivated account uses this email address, an activation email has been sent to it"}

	manager, err := app.models.Managers.GetByEmail(ctx, input.Email)
	switch {
	case errors.Is(err, data.ErrRecordNotFound):
		app.writeAccepted(c, accepted)
		return
	case err != nil:
		app.serverErrorResponse(c, err)
		return
	}

	if manager.Status != data.ManagerStatusPending {
		app.writeAccepted(c, accepted)
		return
	}

	if err := app.models.Tokens.DeleteAllForManager(ctx, data.ScopeActivation, manager.ID); err != nil {
		app.serverErrorResponse(c, err)
		return
	}

	token, err := app.models.Tokens.New(ctx, manager.ID, activationTokenTTL, data.ScopeActivation)
	if err != nil {
		app.serverErrorResponse(c, err)
		return
	}

	app.sendActivationEmail(manager, token)
	app.writeAccepted(c, accepted)
}

// passwordResetTokenTTL is how long an emailed reset code stays valid.
const passwordResetTokenTTL = 45 * time.Minute

// createPasswordResetTokenHandler handles POST /v1/tokens/password-reset:
// {"email": ...}. For an active manager, any earlier reset codes are
// revoked and a new one (45 minutes) is emailed in the background with
// password_reset.tmpl.
//
// Like the activation resend, it always answers the same 202, so it can't
// be used to discover which emails are registered. Pending and suspended
// accounts get no email: a pending account should activate first, and a
// reset must never be a way back into a suspended one.
func (app *application) createPasswordResetTokenHandler(c *gin.Context) {
	var input struct {
		Email string `json:"email" validate:"required,email"`
	}

	if err := app.readJSON(c, &input); err != nil {
		app.badRequestResponse(c, err)
		return
	}

	v := validator.New()
	if v.Struct(input); !v.Valid() {
		app.failedValidationResponse(c, v.Errors)
		return
	}

	ctx := c.Request.Context()
	accepted := envelope{"message": "if an active account uses this email address, password reset instructions have been sent to it"}

	manager, err := app.models.Managers.GetByEmail(ctx, input.Email)
	switch {
	case errors.Is(err, data.ErrRecordNotFound):
		app.writeAccepted(c, accepted)
		return
	case err != nil:
		app.serverErrorResponse(c, err)
		return
	}

	if manager.Status != data.ManagerStatusActive {
		app.writeAccepted(c, accepted)
		return
	}

	if err := app.models.Tokens.DeleteAllForManager(ctx, data.ScopePasswordReset, manager.ID); err != nil {
		app.serverErrorResponse(c, err)
		return
	}

	token, err := app.models.Tokens.New(ctx, manager.ID, passwordResetTokenTTL, data.ScopePasswordReset)
	if err != nil {
		app.serverErrorResponse(c, err)
		return
	}

	emailData := map[string]any{
		"FirmName":   manager.FirmName,
		"ResetURL":   app.config.frontendURL + "/auth/reset-password?token=" + token.Plaintext,
		"ResetToken": token.Plaintext,
		"ExpiresIn":  humanDuration(passwordResetTokenTTL),
	}

	app.background(func() {
		if err := app.mailer.Send(manager.Email, "password_reset.tmpl", emailData); err != nil {
			app.logger.Error("sending password reset email", "manager_id", manager.ID, "error", err)
		}
	})

	app.writeAccepted(c, accepted)
}

// writeAccepted responds 202 with data.
func (app *application) writeAccepted(c *gin.Context, data envelope) {
	if err := app.writeJSON(c, http.StatusAccepted, data, nil); err != nil {
		app.serverErrorResponse(c, err)
	}
}
