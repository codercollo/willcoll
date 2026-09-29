package main

import (
	"errors"
	"net/http"

	"github.com/codercollo/willcoll/backend/internal/data"
	"github.com/codercollo/willcoll/backend/internal/validator"
	"github.com/gin-gonic/gin"
)

// Login and logout (system-design.txt 4.4, ADR 0001). Greenlight ch.15
// issues bearer tokens from a tokens table; Willcoll signs managers and
// the Super Admin into SCS server-side sessions instead, so a session can
// be revoked instantly by deleting its row. The session cookie carries the
// principal (manager or admin); authenticate (middleware.go) resolves it
// on every request.

// loginInput is the body of both login endpoints.
type loginInput struct {
	Email    string `json:"email" validate:"required,email"`
	Password string `json:"password" validate:"required"`
}

func (app *application) readLogin(c *gin.Context) (loginInput, bool) {
	var input loginInput

	if err := app.readJSON(c, &input); err != nil {
		app.badRequestResponse(c, err)
		return input, false
	}

	v := validator.New()
	v.Struct(input)
	v.Check(len(input.Password) <= 72, "password", "must not be more than 72 bytes long")
	if !v.Valid() {
		app.failedValidationResponse(c, v.Errors)
		return input, false
	}

	return input, true
}

// createSessionHandler handles POST /v1/sessions: a manager signs in with
// email and password. Pending (unactivated) managers may sign in; the
// dashboard routes then answer 403 until they activate. Suspended managers
// get no session.
func (app *application) createSessionHandler(c *gin.Context) {
	input, ok := app.readLogin(c)
	if !ok {
		return
	}

	manager, err := app.models.Managers.GetByEmail(c.Request.Context(), input.Email)
	if err != nil {
		switch {
		case errors.Is(err, data.ErrRecordNotFound):
			data.SpendPasswordCheck(input.Password)
			app.invalidCredentialsResponse(c)
		default:
			app.serverErrorResponse(c, err)
		}
		return
	}

	match, err := manager.Password.Matches(input.Password)
	if err != nil {
		app.serverErrorResponse(c, err)
		return
	}
	if !match {
		app.invalidCredentialsResponse(c)
		return
	}

	if manager.Status == data.ManagerStatusSuspended {
		app.accountSuspendedResponse(c)
		return
	}

	if err := app.loginPrincipal(c, principalManager, manager.ID); err != nil {
		app.serverErrorResponse(c, err)
		return
	}

	if err := app.writeJSON(c, http.StatusOK, envelope{"manager": manager}, nil); err != nil {
		app.serverErrorResponse(c, err)
	}
}

// createAdminSessionHandler handles POST /v1/admin/sessions: the Super
// Admin signs in with the credentials from .env (ADMIN_EMAIL /
// ADMIN_PASSWORD). A separate endpoint from manager login, so a manager
// and the admin can never be confused for one another.
func (app *application) createAdminSessionHandler(c *gin.Context) {
	input, ok := app.readLogin(c)
	if !ok {
		return
	}

	admin, err := app.models.Admins.GetByEmail(c.Request.Context(), input.Email)
	if err != nil {
		switch {
		case errors.Is(err, data.ErrRecordNotFound):
			data.SpendPasswordCheck(input.Password)
			app.invalidCredentialsResponse(c)
		default:
			app.serverErrorResponse(c, err)
		}
		return
	}

	match, err := admin.Password.Matches(input.Password)
	if err != nil {
		app.serverErrorResponse(c, err)
		return
	}
	if !match {
		app.invalidCredentialsResponse(c)
		return
	}

	if err := app.loginPrincipal(c, principalAdmin, admin.ID); err != nil {
		app.serverErrorResponse(c, err)
		return
	}

	if err := app.writeJSON(c, http.StatusOK, envelope{"admin": admin}, nil); err != nil {
		app.serverErrorResponse(c, err)
	}
}

// showSessionHandler handles GET /v1/sessions: who is signed in. The
// frontend's role-based route guard (system-design.txt 6.4) calls it to
// decide between the manager dashboard, the admin pages and the login page.
func (app *application) showSessionHandler(c *gin.Context) {
	var body envelope

	if admin, ok := contextGetAdmin(c); ok {
		body = envelope{"principal_type": principalAdmin, "admin": admin}
	} else if manager := contextGetManager(c); !manager.IsAnonymous() {
		body = envelope{"principal_type": principalManager, "manager": manager, "access": app.accessFor(c, manager)}
	} else {
		app.authenticationRequiredResponse(c)
		return
	}

	if err := app.writeJSON(c, http.StatusOK, body, nil); err != nil {
		app.serverErrorResponse(c, err)
	}
}

// deleteSessionHandler handles DELETE /v1/sessions: sign out, manager or
// admin. The session row is deleted and the cookie cleared.
func (app *application) deleteSessionHandler(c *gin.Context) {
	if err := app.logoutPrincipal(c); err != nil {
		app.serverErrorResponse(c, err)
		return
	}

	if err := app.writeJSON(c, http.StatusOK, envelope{"message": "you have been signed out"}, nil); err != nil {
		app.serverErrorResponse(c, err)
	}
}
