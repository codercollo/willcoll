package main

import (
	"net/http"
	"time"

	"github.com/codercollo/willcoll/backend/internal/shared/auth"
	"github.com/codercollo/willcoll/backend/internal/shared/httpx"
	"github.com/codercollo/willcoll/backend/internal/shared/mailer"
	"github.com/gin-gonic/gin"
)

// Step-up authentication (docs/security/authorization.md, D5). The window is
// opened by platform-api (POST /v1/sessions/step-up, the only binary that
// writes sessions) and read here from the shared platform session by
// resolvePlatformPrincipal, which puts it on the context.

// stepUpSatisfied reports whether this request's session has an open window.
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
	if manager.IsAnonymous() || manager.Email == "" {
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
