package main

import (
	"context"
	"time"

	"github.com/codercollo/willcoll/backend/internal/platform/data"
)

// Outbox worker (docs/security/c3-spec.md C3.7). Owner alerts are queued in
// email_outbox by whichever API raised them (platform-api or a product API)
// and delivered here, the one binary with the mailer. A failed send is
// retried with backoff (data.OutboxRetryDelay) up to data.OutboxMaxAttempts,
// then marked 'dead' (never retried, logged, counted on the System page). A crash between sending and settling
// re-sends once the claim's lease lapses: delivery is at least once, enqueue is
// idempotent per event.

const (
	outboxPollInterval = 15 * time.Second
	outboxBatch        = 20
	outboxSendTimeout  = 30 * time.Second
)

// startOutboxWorker runs deliverOutbox every outboxPollInterval until ctx is
// cancelled (server.go's shutdown).
func (app *application) startOutboxWorker(ctx context.Context) {
	go func() {
		app.deliverOutbox(ctx) // a restart delivers what was queued meanwhile at once

		ticker := time.NewTicker(outboxPollInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				app.deliverOutbox(ctx)
			}
		}
	}()
}

// deliverOutbox claims due emails and sends them, settling each one. It
// returns how many were sent. Errors are logged per email and never stop the
// batch: one bad recipient must not hold up everyone behind it.
func (app *application) deliverOutbox(ctx context.Context) int {
	claimCtx, cancel := context.WithTimeout(ctx, outboxSendTimeout)
	emails, err := app.models.Outbox.ClaimDue(claimCtx, outboxBatch)
	cancel()
	if err != nil {
		app.logger.Error("email outbox: claiming due emails", "error", err)
		return 0
	}

	sent := 0
	for _, e := range emails {
		sendErr := app.mailer.Send(e.Recipient, e.Template, e.Payload)

		settleCtx, settleCancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		if sendErr == nil {
			if err := app.models.Outbox.MarkSent(settleCtx, e.ID); err != nil {
				// Sent but not recorded: the lease lapses and it is sent again.
				app.logger.Error("email outbox: marking an email sent", "outbox_id", e.ID, "error", err)
			}
			sent++
		} else {
			// Logged without the recipient's address or the payload.
			app.logger.Error("email outbox: delivery failed", "outbox_id", e.ID, "manager_id", e.ManagerID,
				"template", e.Template, "attempt", e.Attempts, "error", sendErr)
			if err := app.models.Outbox.MarkFailed(settleCtx, e.ID, e.Attempts, sendErr); err != nil {
				app.logger.Error("email outbox: recording a failed delivery", "outbox_id", e.ID, "error", err)
			} else if e.Attempts >= data.OutboxMaxAttempts {
				app.logger.Error("email outbox: alert is dead, out of attempts and will not be retried",
					"outbox_id", e.ID, "manager_id", e.ManagerID, "template", e.Template, "attempts", e.Attempts)
			}
		}
		settleCancel()
	}
	return sent
}
