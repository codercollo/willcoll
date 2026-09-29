package main

import (
	"errors"
	"net/http"
	"strings"

	"github.com/codercollo/willcoll/backend/internal/data"
	"github.com/codercollo/willcoll/backend/internal/validator"
	"github.com/gin-gonic/gin"
)

// The /account page (system-design.txt 6.1): the firm's own profile and
// password. There is one login per firm, so "the account" is the signed-in
// manager. Email and username are the sign-in identity and are not editable
// here: changing them needs re-verification, which v1 does not offer.

// showAccountHandler handles GET /v1/account.
func (app *application) showAccountHandler(c *gin.Context) {
	manager := contextGetManager(c)
	if manager.IsAnonymous() {
		app.authenticationRequiredResponse(c)
		return
	}
	if err := app.writeJSON(c, http.StatusOK, envelope{"manager": manager, "access": app.accessFor(c, manager)}, nil); err != nil {
		app.serverErrorResponse(c, err)
	}
}

// updateAccountHandler handles PATCH /v1/account. Body: {"firm_name",
// "phone"}, either optional. An omitted field is left alone.
func (app *application) updateAccountHandler(c *gin.Context) {
	current := contextGetManager(c)
	if current.IsAnonymous() {
		app.authenticationRequiredResponse(c)
		return
	}

	var input struct {
		FirmName *string `json:"firm_name"`
		Phone    *string `json:"phone"`
	}
	if err := app.readJSON(c, &input); err != nil {
		app.badRequestResponse(c, err)
		return
	}

	manager := *current // edit a copy: the request context keeps the stored one
	if input.FirmName != nil {
		manager.FirmName = strings.TrimSpace(*input.FirmName)
	}
	if input.Phone != nil {
		manager.Phone = strings.TrimSpace(*input.Phone)
	}

	v := validator.New()
	if data.ValidateManager(v, &manager); !v.Valid() {
		app.failedValidationResponse(c, v.Errors)
		return
	}

	if err := app.models.Managers.Update(c.Request.Context(), &manager); err != nil {
		switch {
		case errors.Is(err, data.ErrEditConflict):
			app.editConflictResponse(c)
		default:
			app.serverErrorResponse(c, err)
		}
		return
	}
	if err := app.writeJSON(c, http.StatusOK, envelope{"manager": &manager}, nil); err != nil {
		app.serverErrorResponse(c, err)
	}
}

// changePasswordHandler handles PUT /v1/account/password. Body:
// {"current_password", "new_password"}. The current password must be right
// (a stolen session alone cannot take the account over). On success every
// other session of the firm is signed out and this one stays signed in.
func (app *application) changePasswordHandler(c *gin.Context) {
	current := contextGetManager(c)
	if current.IsAnonymous() {
		app.authenticationRequiredResponse(c)
		return
	}

	var input struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	if err := app.readJSON(c, &input); err != nil {
		app.badRequestResponse(c, err)
		return
	}

	v := validator.New()
	v.Check(input.CurrentPassword != "", "current_password", "must be provided")
	v.Check(input.NewPassword != current.Username, "new_password", "must not be your username")
	data.ValidateManagerPasswordPlaintext(v, input.NewPassword)
	if !v.Valid() {
		app.failedValidationResponse(c, v.Errors)
		return
	}

	matches, err := current.Password.Matches(input.CurrentPassword)
	if err != nil {
		app.serverErrorResponse(c, err)
		return
	}
	if !matches {
		// A validation error, not a 401: the session is fine, the field is wrong.
		app.failedValidationResponse(c, map[string]string{"current_password": "is incorrect"})
		return
	}
	if input.NewPassword == input.CurrentPassword {
		app.failedValidationResponse(c, map[string]string{"new_password": "must be different from the current password"})
		return
	}

	manager := *current
	if err := manager.Password.Set(input.NewPassword); err != nil {
		app.serverErrorResponse(c, err)
		return
	}

	ctx := c.Request.Context()
	if err := app.models.Managers.Update(ctx, &manager); err != nil {
		switch {
		case errors.Is(err, data.ErrEditConflict):
			app.editConflictResponse(c)
		default:
			app.serverErrorResponse(c, err)
		}
		return
	}

	// Outstanding activation and reset links must not outlive the old password.
	if err := app.models.Tokens.DeleteEveryScopeForManager(ctx, manager.ID); err != nil {
		app.serverErrorResponse(c, err)
		return
	}
	if _, err := app.revokeManagerSessions(ctx, manager.ID); err != nil {
		app.serverErrorResponse(c, err)
		return
	}
	// Revoking ended this session too; start a fresh one so the user who just
	// changed their password is not thrown out.
	if err := app.loginPrincipal(c, principalManager, manager.ID); err != nil {
		app.serverErrorResponse(c, err)
		return
	}

	if err := app.writeJSON(c, http.StatusOK, envelope{"message": "your password was changed; other devices were signed out"}, nil); err != nil {
		app.serverErrorResponse(c, err)
	}
}
