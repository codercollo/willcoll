package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"time"

	platformdata "github.com/codercollo/willcoll/backend/internal/platform/data"
	"github.com/codercollo/willcoll/backend/internal/rentals/data"
	"github.com/codercollo/willcoll/backend/internal/shared/payhero"
	"github.com/codercollo/willcoll/backend/internal/shared/sms"
	"github.com/codercollo/willcoll/backend/pkg/moneyfmt"
	"github.com/gin-gonic/gin"
)

const (
	maxWebhookBody        = 1 << 20
	webhookProcessTimeout = 30 * time.Second
	webhookAttempts       = 3

	// inboxSweepInterval/inboxStaleAfter are the 3g-3 sweeper's own
	// schedule: every minute, re-drive any payment_inbox row still
	// 'claimed' more than 5 minutes after it was claimed — a process that
	// crashed (or was killed) between claiming and posting locally.
	inboxSweepInterval = time.Minute
	inboxStaleAfter    = 5 * time.Minute
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

// processCollections is the inbox-aware entry point for one callback
// (build-phases.txt 3g-2): a successful payment is claimed in
// willcoll_platform's payment_inbox BEFORE it is posted locally, so two
// products can never both apply the same M-Pesa receipt, and a crash
// between claim and post leaves a durable 'claimed' row the sweeper
// (3g-3) can finish rather than losing the money's trail entirely.
//
// A failed/cancelled push (no receipt, nothing to dedupe) and a platform
// connection going down both skip the inbox and post locally exactly as
// before 3g-2 — collections must never stop because platform is
// unreachable; an unreachable platform is only ever counted
// (app.inboxFallbacks) for the health page, never a reason to drop money.
func (app *application) processCollections(payload *payhero.CollectionsWebhookPayload) {
	app.processCollectionsAs(payload, "")
}

// processCollectionsAs is processCollections on behalf of a background job
// (the inbox sweeper): the firm it acts for is the channel claim's, and the
// re-drive is audited for that firm as the named system actor (C3.4).
func (app *application) processCollectionsAs(payload *payhero.CollectionsWebhookPayload, job string) {
	if !payload.Success || app.platform == nil {
		app.postCollectionsLocally(payload)
		return
	}
	amount, err := moneyfmt.Parse(payload.Amount)
	if err != nil {
		// Let postCollectionsLocally produce the same ErrInvalidInput it
		// always has for a malformed amount — nothing valid to claim.
		app.postCollectionsLocally(payload)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), webhookProcessTimeout)
	claim, claimErr := app.platform.Inbox.ClaimChannel(ctx, payload.MpesaReceipt, payload.ChannelID, platformdata.ProductRentals, amount, payload.Msisdn, payload.Raw)
	cancel()

	switch {
	case claimErr == nil:
		// The firm comes from the channel's claim, never from the payload
		// (C3.2): the pipeline refuses a payment that resolves elsewhere.
		payload.ClaimedManagerID = claim.ManagerID
		if job != "" {
			app.auditSystem(job, claim.ManagerID, "payment_inbox.redrive", "payment", nil, "re-drove a stale claim, receipt "+payload.MpesaReceipt)
		}
		// Claimed just now, or still 'claimed' from an earlier attempt
		// that crashed before posting: proceed exactly as today, then
		// mark posted so a redelivery or the sweeper never reprocesses it.
		if app.postCollectionsLocally(payload) {
			markCtx, markCancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer markCancel()
			if err := app.platform.Inbox.MarkPosted(markCtx, payload.MpesaReceipt); err != nil {
				app.logger.Error("payment_inbox: marking posted failed", "receipt", payload.MpesaReceipt, "error", err)
			}
		}
	case errors.Is(claimErr, platformdata.ErrAlreadyPosted):
		app.logger.Info("payment_inbox: already posted; acking without reprocessing", "receipt", payload.MpesaReceipt)
	case errors.Is(claimErr, platformdata.ErrWrongProduct):
		app.logger.Error("payment_inbox: ALERT receipt or channel claimed by a different product; refusing to post",
			"receipt", payload.MpesaReceipt, "channel", payload.ChannelID)
	case errors.Is(claimErr, platformdata.ErrChannelUnclaimed):
		// The channel has no claim in platform (a misconfiguration, e.g. a
		// hand-seeded channel, or an attacker-chosen channel_id). Nothing is
		// posted (C3.2): the posting product and firm come only from a claim.
		// The money is recorded durably in platform's unclaimed_receipts first
		// (recordUnclaimedReceipt: idempotent per receipt, open on the System
		// page until a Super Admin resolves it). The alarm (ERROR log with the
		// full payload, plus the health page list) is only the notice.
		app.recordUnclaimedReceipt(payload, amount)
		app.alarmUnclaimedChannel(payload)
	default:
		app.logger.Warn("payment_inbox: claim unavailable; falling back to local-only posting",
			"receipt", payload.MpesaReceipt, "error", claimErr)
		app.inboxFallbacks.record(time.Now())
		app.postCollectionsLocally(payload)
	}
}

// postCollectionsLocally runs the collections pipeline exactly as it ran
// before 3g-2: retrying transient failures, texting the tenant once their
// payment is applied. Nothing here can reach PayHero any more (it was
// already acknowledged), so a payment that still fails is logged in full
// for manual recovery — this includes an unroutable payment (unknown
// channel): the channel/tenant resolution is unchanged by 3g-2, so that
// case is still just logged, never dropped.
//
// Returns true when the payment was successfully recorded locally
// (whatever its outcome — applied, unmatched for review, or a redelivery),
// so the caller knows whether the inbox claim may be marked posted.
func (app *application) postCollectionsLocally(payload *payhero.CollectionsWebhookPayload) bool {
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
		return false
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
	return true
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
		errors.Is(err, data.ErrAllocationMismatch)
}

// startInboxSweeperJob runs the payment_inbox sweeper every
// inboxSweepInterval (build-phases.txt 3g-3): a safety net for a process
// that crashed, was killed, or otherwise died between claiming a receipt
// and posting it locally, leaving a row stuck 'claimed'. Stops when ctx is
// cancelled (server.go's shutdown). A no-op, never started, with no
// platform connection — there is no inbox to sweep.
func (app *application) startInboxSweeperJob(ctx context.Context) {
	if app.platform == nil {
		return
	}

	go func() {
		ticker := time.NewTicker(inboxSweepInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				app.sweepStaleInbox(ctx)
			}
		}
	}()
}

// sweepStaleInbox re-drives every rentals payment_inbox row still 'claimed'
// after inboxStaleAfter, through the exact same processCollections path a
// fresh webhook delivery would take: Claim re-reads the row (still
// 'claimed' under this product, since ON CONFLICT DO NOTHING leaves it
// untouched) and returns nil, so posting and marking proceed normally.
// This is what makes a redelivery, a sweeper pass, and a concurrent double
// delivery all converge on exactly one posting — the local pipeline's own
// mpesa_receipt uniqueness (CreatePayment's ON CONFLICT) is the final
// guard no matter how many times a receipt is re-driven.
func (app *application) sweepStaleInbox(ctx context.Context) {
	listCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	stale, err := app.platform.Inbox.ListStale(listCtx, platformdata.ProductRentals, moneyfmt.Now().Add(-inboxStaleAfter))
	cancel()
	if err != nil {
		app.logger.Error("payment_inbox sweep: listing stale claims", "error", err)
		return
	}

	for _, claim := range stale {
		payload, err := payhero.ParseCollectionsPayload(claim.RawPayload)
		if err != nil {
			// The row was claimed from a payload this version of
			// ParseCollectionsPayload can no longer read — a schema
			// change, not something the sweeper can recover from by
			// itself. Logged for manual recovery; it stays 'claimed' and
			// is picked up again next sweep (and keeps being logged)
			// until someone fixes or clears it, rather than silently
			// stuck with no trace.
			app.logger.Error("payment_inbox sweep: stale claim's payload is unparsable",
				"receipt", claim.MpesaReceipt, "claimed_at", claim.ClaimedAt, "error", err)
			continue
		}
		// The channel actually used at claim time, not whatever the
		// original request's query string or config default was — there
		// is no HTTP request to read either from here.
		payload.ChannelID = claim.ChannelID

		app.logger.Info("payment_inbox sweep: re-driving a stale claim", "receipt", claim.MpesaReceipt, "claimed_at", claim.ClaimedAt)
		app.processCollectionsAs(payload, jobInboxSweeper)
	}
}
