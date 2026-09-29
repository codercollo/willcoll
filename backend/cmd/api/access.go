package main

import (
	"net/http"
	"strings"
	"time"

	"github.com/codercollo/willcoll/backend/internal/data"
	"github.com/gin-gonic/gin"
)

// Free trial and subscription gate.
//
// A firm gets a free trial (TRIAL_DAYS, default 7) from the moment it
// activates. After that it must buy a plan: every manager route answers 402
// Payment Required, except the ones needed to pay (/account and /billing).
// The public tenant pay page and the PayHero webhooks are outside this gate,
// so a lapsed firm's tenants can still pay their rent and that money is still
// recorded.

// gateExemptPrefixes are the manager routes that stay open to a firm whose
// trial has ended, so it can sign in, see why, and pay.
var gateExemptPrefixes = []string{"/v1/account", "/v1/billing"}

func gateExempt(fullPath string) bool {
	for _, p := range gateExemptPrefixes {
		if fullPath == p || strings.HasPrefix(fullPath, p+"/") {
			return true
		}
	}
	return false
}

// trialEndsAt is when a trial that starts now would end.
func (app *application) trialEndsAt(start time.Time) time.Time {
	return data.TrialEnd(start, app.config.trialDays)
}

// subscriptionRequiredResponse is the paywall: 402, in the usual envelope.
func (app *application) subscriptionRequiredResponse(c *gin.Context) {
	app.errorResponse(c, http.StatusPaymentRequired, "your free trial has ended: choose a plan to keep using Willcoll")
}

// requireSubscription closes the dashboard to a firm that is neither on a live
// subscription nor inside its free trial. It runs after requireActivatedManager.
func (app *application) requireSubscription() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !app.config.subscriptionGate || gateExempt(c.FullPath()) {
			c.Next()
			return
		}
		manager := contextGetManager(c)
		if manager.IsAnonymous() { // requireActivatedManager already answered
			c.Next()
			return
		}
		access, err := app.models.Billing.AccessFor(c.Request.Context(), manager)
		if err != nil {
			app.serverErrorResponse(c, err)
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

// accessFor returns a manager's standing for a response body. A failure is
// logged and treated as "unknown" rather than breaking the page that asked.
func (app *application) accessFor(c *gin.Context, manager *data.Manager) *data.Access {
	if !app.config.subscriptionGate {
		return nil
	}
	access, err := app.models.Billing.AccessFor(c.Request.Context(), manager)
	if err != nil {
		app.logError(c, err)
		return nil
	}
	return &access
}
