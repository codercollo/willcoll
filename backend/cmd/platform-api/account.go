package main

import (
	"errors"
	"net/http"
	"strings"

	"github.com/codercollo/willcoll/backend/internal/platform/data"
	"github.com/codercollo/willcoll/backend/internal/shared/validator"
	"github.com/gin-gonic/gin"
)

// The /account page: the firm's own profile and password. There is one
// login per firm, so "the account" is the signed-in manager. Email and
// username are the sign-in identity and are not editable here: changing
// them needs re-verification, which v1 does not offer.

// showAccountHandler handles GET /v1/account.
//
// KNOWN GAP (build-phases.txt Phase 3 step 3c-iii): the old rentals-api
// response also included an "access" key (trial/subscription paywall
// state). Dropped here until billing moves to platform-api — see
// sessions.go's showSessionHandler for the same note.
func (app *application) showAccountHandler(c *gin.Context) {
	manager := contextGetManager(c)
	if manager.IsAnonymous() {
		app.authenticationRequiredResponse(c)
		return
	}
	if err := app.writeJSON(c, http.StatusOK, envelope{"manager": manager}, nil); err != nil {
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
	app.audit(c, "", "account.updated", "manager", &manager.ID, "")
	if err := app.writeJSON(c, http.StatusOK, envelope{"manager": &manager}, nil); err != nil {
		app.serverErrorResponse(c, err)
	}
}

// changePasswordHandler handles PUT /v1/account/password. Body:
// {"current_password", "new_password"}. The current password must be right
// (a stolen session alone cannot take the account over). On success every
// other session of the firm is signed out, on every product
// (system-design.txt A.4), and this one stays signed in.
func (app *application) changePasswordHandler(c *gin.Context) {
	current := contextGetManager(c)
	if current.IsAnonymous() {
		app.authenticationRequiredResponse(c)
		return
	}
	// A staff session also resolves to its firm's manager row; this route
	// changes the FIRM login's password, never a staff member's (their own
	// is PUT /v1/staff/me/password).
	if !contextGetStaff(c).IsAnonymous() {
		app.notPermittedResponse(c)
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

	app.audit(c, "", "account.password_changed", "manager", &manager.ID, "")
	if err := app.writeJSON(c, http.StatusOK, envelope{"message": "your password was changed; other devices were signed out"}, nil); err != nil {
		app.serverErrorResponse(c, err)
	}
}

// changeStaffPasswordHandler handles PUT /v1/staff/me/password. Body:
// {"current_password", "new_password"}. The signed-in staff member changes
// their OWN password (build-phases.txt 3l follow-up): the current one must be
// right, every other session of theirs is signed out, this one stays.
// The firm login's password is untouched; see changePasswordHandler.
func (app *application) changeStaffPasswordHandler(c *gin.Context) {
	tenantID, ok := contextGetTenantID(c)
	current := contextGetStaff(c)
	if !ok || current.IsAnonymous() {
		// Not signed in, or signed in as the firm login / Super Admin: there
		// is no staff password to change.
		if contextGetManager(c).IsAnonymous() {
			app.authenticationRequiredResponse(c)
		} else {
			app.notPermittedResponse(c)
		}
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
	v.Check(input.NewPassword != current.Email, "new_password", "must not be your email")
	// The shared password rules report under "password"; this request's field is new_password.
	rules := validator.New()
	data.ValidateManagerPasswordPlaintext(rules, input.NewPassword)
	if msg, bad := rules.Errors["password"]; bad {
		v.AddError("new_password", msg)
	}
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
		app.failedValidationResponse(c, map[string]string{"current_password": "is incorrect"})
		return
	}
	if input.NewPassword == input.CurrentPassword {
		app.failedValidationResponse(c, map[string]string{"new_password": "must be different from the current password"})
		return
	}

	staff := *current
	if err := staff.Password.Set(input.NewPassword); err != nil {
		app.serverErrorResponse(c, err)
		return
	}

	ctx := c.Request.Context()
	if err := app.models.Staff.Update(ctx, tenantID, &staff); err != nil {
		switch {
		case errors.Is(err, data.ErrEditConflict):
			app.editConflictResponse(c)
		default:
			app.serverErrorResponse(c, err)
		}
		return
	}

	if _, err := app.revokeStaffSessions(ctx, staff.ID); err != nil {
		app.serverErrorResponse(c, err)
		return
	}
	// Revoking ended this session too; start a fresh one so the person who
	// just changed their password is not thrown out.
	if err := app.loginPrincipal(c, principalStaff, staff.ID); err != nil {
		app.serverErrorResponse(c, err)
		return
	}

	app.audit(c, "", "account.password_changed", "staff", &staff.ID, "")
	if err := app.writeJSON(c, http.StatusOK, envelope{"message": "your password was changed; other devices were signed out"}, nil); err != nil {
		app.serverErrorResponse(c, err)
	}
}
