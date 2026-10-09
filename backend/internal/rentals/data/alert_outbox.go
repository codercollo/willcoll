package data

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/codercollo/willcoll/backend/internal/rentals/db"
	"github.com/codercollo/willcoll/backend/internal/rentals/db/sqlc"
	"github.com/google/uuid"
)

// Owner-alert outbox, Rentals side (migration 000049, C4.0).
//
// A Rentals action that must tell the firm's owner (a charge reversal, a
// payment-channel change) writes its alert to alert_outbox in the SAME
// transaction as the action: they commit together or not at all. A relay job
// (cmd/rentals-api/alert_relay.go) then moves pending rows to the platform's
// email_outbox, whose enqueue is idempotent per (firm, event key), and marks
// them relayed. The two databases cannot share a transaction; this is the
// standard transactional-outbox answer to that. A crash after the commit
// loses nothing (the row is still pending), and a crash between the platform
// enqueue and the mark is harmless (the next relay's enqueue is a no-op).

// TxHook is extra work a model method runs inside its own transaction, after
// its write and before the commit: how an owner alert is enqueued atomically
// with the action it reports. q is the transaction's querier. An error rolls
// the whole transaction back. The transaction can be retried (serialization
// failures), so a hook must be safe to run again: EnqueueAlertIn is, by its
// event key.
type TxHook func(ctx context.Context, q sqlc.Querier) error

func runTxHooks(ctx context.Context, q sqlc.Querier, hooks []TxHook) error {
	for _, h := range hooks {
		if err := h(ctx, q); err != nil {
			return err
		}
	}
	return nil
}

// AlertPayload is everything the owner email needs, captured when the action
// happened so the relayed email names the real actor, address and time. It
// holds no secrets and no raw phone numbers.
type AlertPayload struct {
	Recipient string    `json:"recipient"`
	FirmName  string    `json:"firm_name"`
	Actor     string    `json:"actor"`
	IP        string    `json:"ip"`
	Headline  string    `json:"headline"`
	Target    string    `json:"target"`
	Link      string    `json:"link"`
	At        time.Time `json:"at"`
}

// EnqueueAlertIn writes an alert into alert_outbox inside the transaction the
// caller already holds (q from Store.ExecTenantTx for tenantID). Idempotent per
// (tenantID, key): a second enqueue of the same event reports queued=false.
func EnqueueAlertIn(ctx context.Context, q sqlc.Querier, tenantID uuid.UUID, key string, p AlertPayload) (queued bool, err error) {
	if key == "" || p.Recipient == "" {
		return false, errors.New("alert outbox: an event key and recipient are required")
	}
	raw, err := json.Marshal(p)
	if err != nil {
		return false, err
	}
	n, err := q.EnqueueAlert(ctx, sqlc.EnqueueAlertParams{TenantID: tenantID, EventKey: key, Payload: raw})
	return n > 0, err
}

// PendingAlert is one unrelayed alert as the relay sees it.
type PendingAlert struct {
	ID       uuid.UUID
	TenantID uuid.UUID
	Key      string
	Payload  AlertPayload
	Attempts int
}

// AlertOutboxStats is the System page's view of the queue.
type AlertOutboxStats struct {
	Pending                 int `json:"pending"`
	OldestPendingAgeSeconds int `json:"oldest_pending_age_seconds"`
}

// AlertOutboxModel is the relay's access to alert_outbox. It is cross-tenant
// and so runs on the admin store (BYPASSRLS); enqueueing is not here, it is
// EnqueueAlertIn inside the action's own tenant transaction.
type AlertOutboxModel struct {
	Store db.Store

	// Timeout bounds each method call; zero means DefaultQueryTimeout.
	Timeout time.Duration
}

// ListPending returns up to batch unrelayed alerts, oldest first.
func (m AlertOutboxModel) ListPending(ctx context.Context, batch int) ([]PendingAlert, error) {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	rows, err := m.Store.ListPendingAlerts(ctx, int32(batch))
	if err != nil {
		return nil, err
	}
	out := make([]PendingAlert, 0, len(rows))
	for _, r := range rows {
		var p AlertPayload
		if err := json.Unmarshal(r.Payload, &p); err != nil {
			// A corrupt payload can never be relayed; leave it pending with
			// the reason, visible on the System page, rather than dropping it.
			_ = m.Store.MarkAlertRelayFailed(ctx, sqlc.MarkAlertRelayFailedParams{ID: r.ID, LastError: strptr("unreadable payload: " + err.Error())})
			continue
		}
		out = append(out, PendingAlert{ID: r.ID, TenantID: r.TenantID, Key: r.EventKey, Payload: p, Attempts: int(r.Attempts)})
	}
	return out, nil
}

// MarkRelayed settles an alert the platform outbox now holds.
func (m AlertOutboxModel) MarkRelayed(ctx context.Context, id uuid.UUID) error {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()
	return m.Store.MarkAlertRelayed(ctx, id)
}

// MarkFailed records why an alert could not be relayed this time. It stays
// pending and is retried on the relay's next tick.
func (m AlertOutboxModel) MarkFailed(ctx context.Context, id uuid.UUID, relayErr error) error {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	msg := relayErr.Error()
	if len(msg) > 500 {
		msg = msg[:500]
	}
	return m.Store.MarkAlertRelayFailed(ctx, sqlc.MarkAlertRelayFailedParams{ID: id, LastError: &msg})
}

// Stats summarises the queue for the System page.
func (m AlertOutboxModel) Stats(ctx context.Context) (*AlertOutboxStats, error) {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	row, err := m.Store.AlertOutboxStats(ctx)
	if err != nil {
		return nil, err
	}
	return &AlertOutboxStats{Pending: int(row.Pending), OldestPendingAgeSeconds: int(row.OldestPendingAgeSeconds)}, nil
}

func strptr(s string) *string { return &s }
