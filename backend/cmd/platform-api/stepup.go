package main

import (
	"context"
	"net/http"
	"time"

	"github.com/codercollo/willcoll/backend/internal/platform/data"
	"github.com/codercollo/willcoll/backend/internal/platform/db/sqlc"
	"github.com/codercollo/willcoll/backend/internal/shared/auth"
	"github.com/codercollo/willcoll/backend/internal/shared/httpx"
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
	admin, isAdmin := contextGetAdmin(c)
	manager := contextGetManager(c)
	switch {
	case !app.sessionLoaded(c):
		app.authenticationRequiredResponse(c)
		return
	case isAdmin:
		// The Super Admin re-enters the ADMIN password: the credential of the
		// session itself, never a firm's. The window lives in the same admin
		// session and dies with it.
	case manager.IsAnonymous():
		app.authenticationRequiredResponse(c)
		return
	case manager.Status != data.ManagerStatusActive:
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
	if isAdmin {
		match, err = admin.Password.Matches(input.Password)
	} else if staff := contextGetStaff(c); !staff.IsAnonymous() {
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
		app.auditAdminAction(c, nil, "session.step_up_failed", "session", nil, "wrong password at step-up")
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
	app.auditAdminAction(c, nil, "session.step_up", "session", nil, "re-entered password")

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

// ownerAlertHook is the owner alert for a security-relevant action by anyone in
// the firm, itself included (D5, C3.7), as a data.TxHook: pass it to the model
// method that performs the action (StaffModel.Invite/Update) and the alert is
// enqueued in email_outbox INSIDE that method's transaction (C4.0, the
// transactional outbox). The action and its alert commit together or not at
// all, so a crash between the two can no longer lose an alert, and a failed
// action leaves none behind. platform-api's outbox worker delivers it with
// retries; a delivery that finally fails shows on the System page.
// kind and entity name the event (e.g. "staff.role", the staff id); with the
// request id they make the alert idempotent, so a retried handler queues it
// once. link is the record's absolute URL, or "".
//
// A hook that fails rolls the action back (500): an unalerted authority change
// is the thing this control exists to prevent. The failure is counted for the
// System page.
func (app *application) ownerAlertHook(c *gin.Context, kind, entity, headline, target, link string) data.TxHook {
	manager := contextGetManager(c)
	if manager.IsAnonymous() || manager.Email == "" {
		return func(context.Context, sqlc.Querier) error { return nil }
	}
	staff := contextGetStaff(c)
	alert := data.OwnerAlert{
		Key:      httpx.RequestIDFrom(c) + ":" + kind + ":" + entity,
		Headline: headline, Target: target, Link: link,
	}
	ip := c.ClientIP()

	return func(ctx context.Context, q sqlc.Querier) error {
		if _, err := data.EnqueueOwnerAlertIn(ctx, q, manager, staff, ip, alert); err != nil {
			app.logger.Error("queueing security alert", "manager_id", manager.ID, "kind", kind, "error", err)
			app.alertFailures.record(time.Now())
			return err
		}
		return nil
	}
}
