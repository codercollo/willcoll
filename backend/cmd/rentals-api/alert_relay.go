package main

import (
	"context"
	"time"

	platformdata "github.com/codercollo/willcoll/backend/internal/platform/data"
	"github.com/codercollo/willcoll/backend/internal/rentals/data"
	"github.com/google/uuid"
)

// The owner-alert relay (C4.0). Rentals writes each owner alert to its own
// alert_outbox in the same transaction as the action; this job moves pending
// rows to willcoll_platform.email_outbox (whose worker sends the email) and
// marks them relayed. The platform enqueue is idempotent per (firm, event key),
// so a relay that crashes between the enqueue and the mark, a second instance,
// or a retry after a platform outage can never produce a second email.

const (
	alertRelayInterval = 15 * time.Second
	alertRelayBatch    = 50
)

// alertSource is the Rentals side of the relay (data.AlertOutboxModel).
type alertSource interface {
	ListPending(ctx context.Context, batch int) ([]data.PendingAlert, error)
	MarkRelayed(ctx context.Context, id uuid.UUID) error
	MarkFailed(ctx context.Context, id uuid.UUID, relayErr error) error
}

// alertSink is the platform side (platformdata.OutboxModel).
type alertSink interface {
	EnqueueRelayed(ctx context.Context, tenantID uuid.UUID, a platformdata.RelayedAlert) (bool, error)
}

// relayAlerts moves one batch of pending alerts. It returns how many were
// settled as relayed. An alert the platform will not take stays pending, with
// the reason recorded, and is retried on the next tick; one failure does not
// stop the others.
func relayAlerts(ctx context.Context, src alertSource, sink alertSink, batch int) (relayed int, failed int, err error) {
	pending, err := src.ListPending(ctx, batch)
	if err != nil {
		return 0, 0, err
	}
	for _, a := range pending {
		_, enqErr := sink.EnqueueRelayed(ctx, a.TenantID, platformdata.RelayedAlert{
			Key: a.Key, Recipient: a.Payload.Recipient, FirmName: a.Payload.FirmName, Actor: a.Payload.Actor,
			IP: a.Payload.IP, Headline: a.Payload.Headline, Target: a.Payload.Target, Link: a.Payload.Link, At: a.Payload.At,
		})
		if enqErr != nil {
			failed++
			_ = src.MarkFailed(ctx, a.ID, enqErr)
			continue
		}
		// queued=false means the platform already holds it (an earlier relay
		// got that far before dying): equally settled.
		if err := src.MarkRelayed(ctx, a.ID); err != nil {
			// Still pending, so the next tick enqueues again (a no-op) and
			// retries the mark; record why for the System page.
			failed++
			_ = src.MarkFailed(ctx, a.ID, err)
			continue
		}
		relayed++
	}
	return relayed, failed, nil
}

// startAlertRelayJob runs the relay every alertRelayInterval until ctx ends.
// It starts with a pass at once, so alerts left pending by a crash are
// delivered as soon as the process is back.
func (app *application) startAlertRelayJob(ctx context.Context) {
	if app.platform == nil {
		return
	}
	go func() {
		app.runAlertRelay(ctx)
		ticker := time.NewTicker(alertRelayInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				app.runAlertRelay(ctx)
			}
		}
	}()
}

func (app *application) runAlertRelay(ctx context.Context) {
	passCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	relayed, failed, err := relayAlerts(passCtx, app.models.Alerts, app.platform.Outbox, alertRelayBatch)
	switch {
	case err != nil:
		app.logger.Error("alert relay: listing pending alerts", "error", err)
		app.alertFailures.record(time.Now())
	case failed > 0:
		app.logger.Error("alert relay: some alerts could not be relayed; they stay pending and are retried", "relayed", relayed, "failed", failed)
		app.alertFailures.record(time.Now())
	case relayed > 0:
		app.logger.Info("alert relay: owner alerts handed to the platform outbox", "relayed", relayed)
	}
}
