package main

import (
	"context"
	"errors"
	"net/http"
	"time"

	platformdata "github.com/codercollo/willcoll/backend/internal/platform/data"
	"github.com/codercollo/willcoll/backend/pkg/moneyfmt"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// requireModule and requirePlatformSubscription are the only entitlement
// gates left (build-phases.txt 3i-b): both read willcoll_platform, and
// both ENFORCE. The old local access path and the GATE_MODE shadow mode
// that compared the two are gone — the zero-mismatch evidence the shadow
// ran to produce is exactly what justified deleting it.
//
// Failing closed, never open: a platform read error answers 503 (the
// check itself failed — never a silent allow, and never a 403 that would
// pretend it's the manager's fault), and a missing manager_modules row
// answers 403 (the manager has no entitlement to this product at all).
//
// The public tenant pay page and the PayHero webhooks are outside these
// gates, so a lapsed firm's tenants can still pay their rent and that
// money is still recorded.

// gateUnavailableResponse answers 503: the platform-backed check itself
// failed, so entitlement is unknown.
func (app *application) gateUnavailableResponse(c *gin.Context) {
	app.errorResponse(c, http.StatusServiceUnavailable, "billing/entitlement check is temporarily unavailable; try again shortly")
}

// subscriptionRequiredResponse is the paywall: 402, in the usual envelope.
func (app *application) subscriptionRequiredResponse(c *gin.Context) {
	app.errorResponse(c, http.StatusPaymentRequired, "your free trial has ended: choose a plan to keep using Willcoll")
}

// requireModule guards product's routes on the manager's platform-side
// entitlement (manager_modules). Must run after requireActivatedManager.
func (app *application) requireModule(product string) gin.HandlerFunc {
	return func(c *gin.Context) {
		manager := contextGetManager(c)
		if manager.IsAnonymous() {
			c.Next() // requireActivatedManager already answered
			return
		}
		if app.platform == nil {
			app.gateUnavailableResponse(c)
			c.Abort()
			return
		}

		ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
		defer cancel()

		mod, err := app.platform.Modules.Get(ctx, manager.ID, product)
		switch {
		case errors.Is(err, platformdata.ErrRecordNotFound):
			app.logger.Warn("requireModule: no manager_modules row", "manager_id", manager.ID, "product", product)
			app.notPermittedResponse(c)
			c.Abort()
		case err != nil:
			app.logger.Error("requireModule: reading manager_modules", "manager_id", manager.ID, "product", product, "error", err)
			app.gateUnavailableResponse(c)
			c.Abort()
		case mod.Status != platformdata.ModuleStatusActive && mod.Status != platformdata.ModuleStatusTrialing:
			app.notPermittedResponse(c)
			c.Abort()
		default:
			c.Next()
		}
	}
}

// requirePlatformSubscription closes the product to a firm that is neither
// on a live subscription nor inside its free trial, both read from
// willcoll_platform: the trial is the product module's own (build-phases.txt
// 3f-3), the subscription is the per-product one. Must run after
// requireActivatedManager.
func (app *application) requirePlatformSubscription(product string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !app.config.subscriptionGate {
			c.Next()
			return
		}
		manager := contextGetManager(c)
		if manager.IsAnonymous() {
			c.Next()
			return
		}
		if app.platform == nil {
			app.gateUnavailableResponse(c)
			c.Abort()
			return
		}

		ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
		defer cancel()

		access, err := app.accessForManager(ctx, manager.ID, product)
		if err != nil {
			app.logger.Error("requirePlatformSubscription: computing access", "manager_id", manager.ID, "product", product, "error", err)
			app.gateUnavailableResponse(c)
			c.Abort()
			return
		}
		if !access.Open() {
			app.subscriptionRequiredResponse(c)
			c.Abort()
			return
		}
		c.Next()
	}
}

// accessForManager is the paywall's one source of truth: willcoll_platform's
// module trial and per-product subscription. A missing module row just
// means no trial to fall back on (requireModule owns the "no entitlement"
// 403); a manager with no subscription yet is simply trial-only.
func (app *application) accessForManager(ctx context.Context, managerID uuid.UUID, product string) (platformdata.Access, error) {
	var trialEndsAt *time.Time
	module, err := app.platform.Modules.Get(ctx, managerID, product)
	switch {
	case err == nil:
		trialEndsAt = module.TrialEndsAt
	case !errors.Is(err, platformdata.ErrRecordNotFound):
		return platformdata.Access{}, err
	}

	var sub *platformdata.PlatformSubscription
	platformSub, err := app.platform.Modules.GetSubscription(ctx, managerID, product)
	switch {
	case err == nil:
		sub = platformSub
	case !errors.Is(err, platformdata.ErrRecordNotFound):
		return platformdata.Access{}, err
	}

	return platformdata.ComputeAccess(moneyfmt.Now(), trialEndsAt, sub), nil
}
