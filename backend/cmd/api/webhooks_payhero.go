package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/codercollo/willcoll/backend/internal/data"
	"github.com/codercollo/willcoll/backend/internal/payhero"
	"github.com/codercollo/willcoll/backend/internal/sms"
	"github.com/gin-gonic/gin"
)

const (
	maxWebhookBody        = 1 << 20
	webhookProcessTimeout = 30 * time.Second
	webhookAttempts       = 3
)

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

// payheroCollectionsWebhookHandler handles POST /v1/webhooks/payhero/collections:
// the outcome of a tenant STK push, or an organic paybill payment.
func (app *application) payheroCollectionsWebhookHandler(c *gin.Context) {
	body, ok := app.readVerifiedWebhook(c)
	if !ok {
		return
	}
	payload, err := payhero.ParseCollectionsPayload(body)
	if err != nil {
		app.badRequestResponse(c, err)
		return
	}

	// An organic payment carries no intent, so the channel says whose it is:
	// from the payload, else the callback URL, else the configured default.
	if payload.ChannelID == "" {
		payload.ChannelID = c.Query("channel_id")
	}
	if payload.ChannelID == "" {
		payload.ChannelID = app.config.payhero.collectionsChannelID
	}

	app.acceptWebhook(c)
	app.background(func() { app.processCollections(payload) })
}

// processCollections runs the collections pipeline for one callback, retrying
// transient failures, and texts the tenant once their payment is applied.
// Nothing here can reach PayHero any more (it was already acknowledged), so a
// payment that still fails is logged in full for manual recovery.
func (app *application) processCollections(payload *payhero.CollectionsWebhookPayload) {
	var result *data.IngestResult
	err := retryWebhook(func(ctx context.Context) error {
		var err error
		result, err = app.models.ProcessCollectionsPayment(ctx, payload)
		return err
	})
	if err != nil {
		app.logger.Error("collections payment not processed",
			"error", err.Error(), "receipt", payload.MpesaReceipt, "reference", payload.AccountReference,
			"amount", payload.Amount, "msisdn", payload.Msisdn, "channel", payload.ChannelID, "payload", string(payload.Raw))
		return
	}

	switch {
	case result.Duplicate:
		app.logger.Info("collections payment redelivered", "receipt", payload.MpesaReceipt)
	case result.NeedsReview:
		app.logger.Info("collections payment needs review", "receipt", payload.MpesaReceipt, "status", result.Status)
	}

	if result.Applied {
		app.sendPaymentConfirmation(result)
	}
}

// sendPaymentConfirmation texts the payer. A failure is logged, never
// retried into a duplicate message, and never undoes the payment.
func (app *application) sendPaymentConfirmation(r *data.IngestResult) {
	if r.Msisdn == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	err := app.sms.SendPaymentConfirmation(ctx, r.Msisdn, sms.PaymentConfirmation{
		Property: r.PropertyName, UnitCode: r.UnitCode, Amount: r.Amount.Display(), Receipt: r.Receipt,
	})
	switch {
	case err == nil:
	case errors.Is(err, sms.ErrNotConfigured):
		app.logger.Debug("payment confirmation not sent: SMS is not configured", "receipt", r.Receipt)
	default:
		app.logger.Warn("payment confirmation not sent", "receipt", r.Receipt, "error", err.Error())
	}
}

// payheroSubscriptionsWebhookHandler handles POST /v1/webhooks/payhero/subscriptions:
// the outcome of a manager subscription payment on the billing channel.
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
		result, err = app.models.ProcessSubscriptionPayment(ctx, payload)
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
	return errors.Is(err, data.ErrUnroutable) ||
		errors.Is(err, data.ErrInvalidInput) ||
		errors.Is(err, data.ErrRecordNotFound) ||
		errors.Is(err, data.ErrUnderpaid) ||
		errors.Is(err, data.ErrAllocationMismatch)
}
