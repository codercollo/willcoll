package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/codercollo/willcoll/backend/internal/platform/data"
	"github.com/codercollo/willcoll/backend/internal/shared/payhero"
	"github.com/gin-gonic/gin"
)

const (
	maxWebhookBody        = 1 << 20
	webhookProcessTimeout = 30 * time.Second
	webhookAttempts       = 3
)

// stkPusher is what billing.go needs from the PayHero client. The real
// client satisfies it; tests substitute a fake.
type stkPusher interface {
	STKPush(ctx context.Context, req payhero.STKPushRequest) (*payhero.STKPushResponse, error)
}

// webhookBackoff is the wait before each retry of background processing.
var webhookBackoff = []time.Duration{250 * time.Millisecond, time.Second}

// readVerifiedWebhook reads the body and authenticates the callback (source
// IP and/or shared secret, see payhero.VerifyWebhook). On failure it writes
// the response and returns ok=false. Webhooks are exempt from the per-IP rate
// limit, so this check is what protects them.
func (app *application) readVerifiedWebhook(c *gin.Context) ([]byte, bool) {
	body, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, maxWebhookBody))
	if err != nil {
		app.badRequestResponse(c, errors.New("unreadable request body"))
		return nil, false
	}

	cfg := payhero.VerifyConfig{AllowedIPs: app.config.payhero.webhookIPs, Secret: app.config.payhero.webhookSecret}
	if err := payhero.VerifyWebhook(c.Request, body, c.ClientIP(), cfg); err != nil {
		app.logger.Warn("payhero webhook refused", "path", c.FullPath(), "ip", c.ClientIP(), "reason", err.Error())
		app.errorResponse(c, http.StatusUnauthorized, "webhook not authorized")
		return nil, false
	}
	return body, true
}

// acceptWebhook acknowledges a verified callback at once. PayHero has a short
// timeout and retries on failure, so the work happens after the response.
func (app *application) acceptWebhook(c *gin.Context) {
	if err := app.writeJSON(c, http.StatusOK, envelope{"status": "accepted"}, nil); err != nil {
		app.logger.Error("webhook acknowledgement failed", "error", err.Error())
	}
}

// payheroSubscriptionsWebhookHandler handles
// POST /v1/webhooks/payhero/subscriptions: the outcome of a manager
// subscription payment on the billing channel. Whichever product API's own
// /v1/webhooks/payhero/subscriptions route received this same callback
// (build-phases.txt 3c-iv-b2, until cutover) calls the identical
// BillingModel.ProcessSubscriptionPayment, so a receipt delivered to both
// settles exactly once.
func (app *application) payheroSubscriptionsWebhookHandler(c *gin.Context) {
	body, ok := app.readVerifiedWebhook(c)
	if !ok {
		return
	}
	payload, err := payhero.ParseSubscriptionsPayload(body)
	if err != nil {
		app.badRequestResponse(c, err)
		return
	}

	app.acceptWebhook(c)
	app.background(func() { app.processSubscription(payload) })
}

func (app *application) processSubscription(payload *payhero.SubscriptionsWebhookPayload) {
	var result *data.SubscriptionResult
	err := retryWebhook(func(ctx context.Context) error {
		var err error
		result, err = app.models.Billing.ProcessSubscriptionPayment(ctx, payload)
		return err
	})
	if err != nil {
		app.logger.Error("subscription payment not processed",
			"error", err.Error(), "receipt", payload.MpesaReceipt, "reference", payload.Reference,
			"amount", payload.Amount, "payload", string(payload.Raw))
		return
	}
	app.logger.Info("subscription callback processed", "invoice", result.InvoiceID.String(), "manager", result.ManagerID.String(),
		"paid", result.Paid, "failed", result.Failed, "duplicate", result.Duplicate)
}

// retryWebhook runs fn, retrying failures that could be transient (a busy or
// briefly unreachable database) but not the ones no retry can fix.
func retryWebhook(fn func(ctx context.Context) error) error {
	var err error
	for attempt := 0; attempt < webhookAttempts; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), webhookProcessTimeout)
		err = fn(ctx)
		cancel()
		if err == nil || isPermanentWebhookError(err) {
			return err
		}
		if attempt < len(webhookBackoff) {
			time.Sleep(webhookBackoff[attempt])
		}
	}
	return err
}

func isPermanentWebhookError(err error) bool {
	return errors.Is(err, data.ErrInvalidInput) ||
		errors.Is(err, data.ErrRecordNotFound) ||
		errors.Is(err, data.ErrUnderpaid)
}

// callbackURL is where PayHero should post an outcome: this API's public
// origin plus the webhook path, carrying the shared secret PayHero cannot
// sign with. Empty when no public origin is configured, in which case
// PayHero uses the callback set on the channel.
func (app *application) callbackURL(path string) string {
	base := app.config.payhero.callbackBaseURL
	if base == "" {
		return ""
	}
	u := base + path
	if app.config.payhero.webhookSecret != "" {
		u += "?" + payhero.TokenQueryParam + "=" + app.config.payhero.webhookSecret
	}
	return u
}

// failPush records a failed STK push against its invoice, and logs
// PayHero's reason (its body) which the client must never see.
func (app *application) failPush(ctx context.Context, err error, mark func(reason string) error) {
	app.logger.Error("STK push failed", "error", err.Error())
	if mErr := mark("payment request could not be sent"); mErr != nil {
		app.logger.Error("could not mark the push failed", "error", mErr.Error())
	}
}

// pushErrorResponse turns a PayHero failure into a client answer that hides
// PayHero internals.
func (app *application) pushErrorResponse(c *gin.Context, err error) {
	var apiErr *payhero.APIError
	switch {
	case errors.Is(err, payhero.ErrNotConfigured):
		app.errorResponse(c, http.StatusServiceUnavailable, "payments are not available right now")
	case errors.As(err, &apiErr) && !apiErr.Retryable():
		app.errorResponse(c, http.StatusBadGateway, "the payment could not be requested; check the phone number and try again")
	default:
		app.errorResponse(c, http.StatusBadGateway, "the payment service is not responding; please try again shortly")
	}
}
