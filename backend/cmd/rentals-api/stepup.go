package main

import (
	"context"
	"net/http"
	"strings"
	"time"

	platformdata "github.com/codercollo/willcoll/backend/internal/platform/data"
	"github.com/codercollo/willcoll/backend/internal/rentals/data"
	"github.com/codercollo/willcoll/backend/internal/rentals/db/sqlc"
	"github.com/codercollo/willcoll/backend/internal/shared/auth"
	"github.com/codercollo/willcoll/backend/internal/shared/httpx"
	"github.com/codercollo/willcoll/backend/pkg/moneyfmt"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
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

// ownerAlertHook is the owner alert for a security-relevant action by anyone in
// the firm, itself included (D5, C3.7), as a data.TxHook: pass it to the model
// method that performs the action and the alert is written to the Rentals
// alert_outbox INSIDE that method's transaction (C4.0, the transactional
// outbox). The action and its alert commit together or not at all, so a crash
// between the two can no longer lose an alert, and a failed action leaves none
// behind. The relay job (alert_relay.go) then moves it to the platform's email
// outbox, whose worker delivers it with retries. kind and entity name the
// event; with the request id they make the alert idempotent. link is a path on
// the Rentals frontend (e.g. "/properties/<id>"), or "".
func (app *application) ownerAlertHook(c *gin.Context, kind, entity, headline, target, link string) data.TxHook {
	key := httpx.RequestIDFrom(c) + ":" + kind + ":" + entity
	return app.alertHookForKey(c, key, headline, target, link)
}

// alertHookForKey is ownerAlertHook with an explicit idempotency key, for an
// event that already has a natural unique id.
func (app *application) alertHookForKey(c *gin.Context, key, headline, target, link string) data.TxHook {
	tenantID, payload, ok := app.alertPayload(c, headline, target, link)
	if !ok {
		return func(context.Context, sqlc.Querier) error { return nil }
	}
	return func(ctx context.Context, q sqlc.Querier) error {
		if _, err := data.EnqueueAlertIn(ctx, q, tenantID, key, payload); err != nil {
			app.logger.Error("queueing security alert", "manager_id", tenantID, "key", key, "error", err)
			app.alertFailures.record(time.Now())
			return err
		}
		return nil
	}
}

// reversalAlertHook is the owner alert for a ledger reversal: keyed by the
// entry, not the request (an entry is reversed once, so the alert is queued once
// however many times the request is retried), and built from the reversal the
// transaction just wrote.
func (app *application) reversalAlertHook(c *gin.Context, entryID uuid.UUID, reason string) data.ReversalHook {
	return func(ctx context.Context, q sqlc.Querier, r data.Reversal) error {
		hook := app.alertHookForKey(c, "ledger_entry.reversed:"+entryID.String(), "A ledger entry was reversed",
			"KES "+r.Amount.Display()+" (entry "+entryID.String()+"), reason: "+strings.TrimSpace(reason),
			"/units/"+r.UnitID.String())
		return hook(ctx, q)
	}
}

// alertPayload captures, at the moment of the action, everything the email
// needs: the recipient (the firm login), who did it, from where, and when. ok
// is false when there is nobody to alert (an anonymous principal, or a firm with
// no email), in which case the hooks do nothing, as before.
func (app *application) alertPayload(c *gin.Context, headline, target, link string) (tenantID uuid.UUID, p data.AlertPayload, ok bool) {
	manager := contextGetManager(c)
	if manager.IsAnonymous() || manager.Email == "" {
		return uuid.Nil, p, false
	}
	p = data.AlertPayload{
		Recipient: manager.Email, FirmName: manager.FirmName,
		Actor: platformdata.OwnerAlertActor(manager, contextGetStaff(c)),
		IP:    c.ClientIP(), Headline: headline, Target: target, At: moneyfmt.Now(),
	}
	if link != "" {
		p.Link = app.config.frontendURL + link
	}
	return manager.ID, p, true
}
