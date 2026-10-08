package main

import (
	"net/http"
	"time"

	"github.com/codercollo/willcoll/backend/internal/platform/data"
	"github.com/codercollo/willcoll/backend/internal/shared/auth"
	"github.com/codercollo/willcoll/backend/internal/shared/httpx"
	"github.com/codercollo/willcoll/backend/internal/shared/mailer"
	"github.com/gin-gonic/gin"
)

// Step-up authentication (docs/security/authorization.md, D5; the window is
// defined in internal/shared/auth/stepup.go). platform-api is the only binary
// that can open one, because it is the only one that writes sessions.

// stepUpHandler handles POST /v1/sessions/step-up: {"password"}. The caller
// re-enters their OWN password (the firm login's, or the staff member's), and
// on a match the session is stamped with a window of auth.StepUpTTL during
// which step-up-gated actions are allowed, in every product.
//
// A wrong password is a 422, not a 401: the session is still good, and the
// frontend treats 401 as "signed out". After auth.StepUpMaxFailures wrong
// re-entries in a row the session is ended (401), so a stolen cookie cannot
// be used to guess the password here.
func (app *application) stepUpHandler(c *gin.Context) {
	if _, isAdmin := contextGetAdmin(c); isAdmin {
		app.notPermittedResponse(c) // the Super Admin has no firm actions to step up for
		return
	}
	manager := contextGetManager(c)
	if manager.IsAnonymous() || !app.sessionLoaded(c) {
		app.authenticationRequiredResponse(c)
		return
	}
	if manager.Status != data.ManagerStatusActive {
		app.inactiveAccountResponse(c)
		return
	}

	var input struct {
		Password string `json:"password"`
	}
	if err := app.readJSON(c, &input); err != nil {
		app.badRequestResponse(c, err)
		return
	}
	if input.Password == "" || len(input.Password) > 72 {
		app.failedValidationResponse(c, map[string]string{"password": "enter your password"})
		return
	}

	// Whose password: a staff member's own, otherwise the firm login's.
	var (
		match bool
		err   error
	)
	if staff := contextGetStaff(c); !staff.IsAnonymous() {
		match, err = staff.Password.Matches(input.Password)
	} else {
		match, err = manager.Password.Matches(input.Password)
	}
	if err != nil {
		app.serverErrorResponse(c, err)
		return
	}

	ctx := c.Request.Context()
	sm := app.sessionManager
	if !match {
		failures := sm.GetInt(ctx, auth.SessionKeyStepUpFailures) + 1
		app.audit(c, "", "session.step_up_failed", "session", nil, "wrong password at step-up")
		if failures >= auth.StepUpMaxFailures {
			if err := sm.Destroy(ctx); err != nil {
				app.serverErrorResponse(c, err)
				return
			}
			app.authenticationRequiredResponse(c)
			return
		}
		sm.Put(ctx, auth.SessionKeyStepUpFailures, failures)
		app.failedValidationResponse(c, map[string]string{"password": "that is not your password"})
		return
	}

	until := auth.StepUpUntil(time.Now())
	sm.Put(ctx, auth.SessionKeyStepUpUntil, until)
	sm.Remove(ctx, auth.SessionKeyStepUpFailures)
	app.audit(c, "", "session.step_up", "session", nil, "re-entered password")

	if err := app.writeJSON(c, http.StatusOK, envelope{"step_up_until": until}, nil); err != nil {
		app.serverErrorResponse(c, err)
	}
}

// stepUpSatisfied reports whether this request's session has an open window.
// authenticate puts the session's value on the context.
func (app *application) stepUpSatisfied(c *gin.Context) bool {
	return auth.StepUpActive(c.GetInt64(auth.StepUpContextKey), time.Now())
}

// requireStepUp refuses (403, code step_up_required) a request whose session
// has no open window. Put it after the permission guard, so a caller who may
// not do the action at all gets the plain 403, not a password prompt.
func (app *application) requireStepUp() gin.HandlerFunc {
	return func(c *gin.Context) {
		if app.stepUpSatisfied(c) {
			c.Next()
			return
		}
		app.stepUpRequiredResponse(c)
		c.Abort()
	}
}

// stepUpRequiredResponse responds 403 with the code a client branches on to
// ask for the password and retry.
func (app *application) stepUpRequiredResponse(c *gin.Context) {
	app.responder().CodedErrorResponse(c, http.StatusForbidden,
		"re-enter your password to continue", auth.StepUpCode, httpx.Envelope{})
}

// alertOwner emails the firm login about a security-relevant action by anyone
// in the firm, itself included (D5). It runs in the background: a mail outage
// must never fail or delay the action, and is logged.
func (app *application) alertOwner(c *gin.Context, headline, target string) {
	manager := contextGetManager(c)
	if manager.IsAnonymous() {
		return
	}
	actor := manager.FirmName + " (firm login)"
	if staff := contextGetStaff(c); !staff.IsAnonymous() {
		actor = staff.Name + " (" + staff.Role + ")"
	}
	alert := mailer.SecurityAlert{Headline: headline, Actor: actor, IP: c.ClientIP(), Target: target, At: time.Now()}
	recipient, firm := manager.Email, manager.FirmName

	app.background(func() {
		if err := app.mailer.Send(recipient, mailer.SecurityAlertTemplate, mailer.SecurityAlertData(firm, alert)); err != nil {
			app.logger.Error("sending security alert", "manager_id", manager.ID, "headline", headline, "error", err)
		}
	})
}
