package data

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/codercollo/willcoll/backend/internal/platform/db"
	"github.com/codercollo/willcoll/backend/internal/platform/db/sqlc"
	"github.com/codercollo/willcoll/backend/internal/shared/mailer"
	"github.com/codercollo/willcoll/backend/pkg/moneyfmt"
	"github.com/google/uuid"
)

// Owner-alert outbox (migration 000022, docs/security/c3-spec.md C3.7).
//
// An alert is written to email_outbox in the same breath as the action that
// raised it, then delivered by platform-api's worker. Enqueueing never calls
// the mailer, so a mail outage cannot fail or slow the action, and a restart
// cannot lose an alert that was queued. Delivery is at least once (a worker
// that dies between sending and settling is retried when its lease lapses);
// enqueueing is idempotent per event.

const (
	OutboxPending = "pending"
	OutboxSent    = "sent"
	OutboxFailed  = "failed"

	// OutboxMaxAttempts is how many deliveries a row gets before it is left
	// 'failed' for the System page.
	OutboxMaxAttempts = 8
	// OutboxLease is how long a claimed row is hidden from other workers.
	OutboxLease = 2 * time.Minute
)

// OutboxRetryDelay is the wait before the next attempt, after the nth one
// failed (1-based): 30s, 2m, 10m, 30m, 1h, 2h, 4h.
func OutboxRetryDelay(attempt int) time.Duration {
	delays := []time.Duration{30 * time.Second, 2 * time.Minute, 10 * time.Minute, 30 * time.Minute, time.Hour, 2 * time.Hour, 4 * time.Hour}
	if attempt < 1 {
		attempt = 1
	}
	if attempt > len(delays) {
		attempt = len(delays)
	}
	return delays[attempt-1]
}

// TxHook is extra work a model method runs inside its own transaction, after
// its write and before the commit (C4.0): the way an owner alert is enqueued
// atomically with the action it reports. q is the transaction's querier. An
// error rolls the whole transaction back.
type TxHook func(ctx context.Context, q sqlc.Querier) error

func runTxHooks(ctx context.Context, q sqlc.Querier, hooks []TxHook) error {
	for _, h := range hooks {
		if err := h(ctx, q); err != nil {
			return err
		}
	}
	return nil
}

// OutboxEmail is one queued email as the worker sees it.
type OutboxEmail struct {
	ID        uuid.UUID
	ManagerID uuid.UUID
	Recipient string
	Template  string
	Payload   map[string]any
	Attempts  int
}

// OutboxStats is the System page's view of the queue.
type OutboxStats struct {
	Pending                 int     `json:"pending"`
	Failed                  int     `json:"failed"`
	Sent24h                 int     `json:"sent_24h"`
	OldestPendingAgeSeconds int     `json:"oldest_pending_age_seconds"`
	LastError               *string `json:"last_error"`
}

// OutboxModel is email_outbox access. Enqueue is tenant-scoped (Store, the app
// pool); Claim, Settle and Stats are cross-tenant and use AdminStore (BYPASSRLS).
type OutboxModel struct {
	Store      db.Store
	AdminStore db.Store

	// Timeout bounds each method call; zero means DefaultQueryTimeout.
	Timeout time.Duration
}

// Enqueue queues one email for tenantID. eventKey makes it idempotent: a second
// enqueue with the same (tenantID, eventKey) queues nothing and reports
// queued=false. payload is the template data and must already be free of
// secrets and masked.
func (m OutboxModel) Enqueue(ctx context.Context, tenantID uuid.UUID, eventKey, recipient, template string, payload map[string]any) (queued bool, err error) {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	err = m.Store.ExecTenantTx(ctx, tenantID, func(q sqlc.Querier) error {
		var err error
		queued, err = EnqueueIn(ctx, q, tenantID, eventKey, recipient, template, payload)
		return err
	})
	return queued, err
}

// EnqueueIn is Enqueue inside a transaction the caller already holds (q from
// Store.ExecTenantTx for tenantID): the alert commits with the action that
// raised it, or not at all. Idempotent per (tenantID, eventKey) like Enqueue.
func EnqueueIn(ctx context.Context, q sqlc.Querier, tenantID uuid.UUID, eventKey, recipient, template string, payload map[string]any) (queued bool, err error) {
	if eventKey == "" || recipient == "" || template == "" {
		return false, errors.New("outbox: an event key, recipient and template are required")
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return false, err
	}
	n, err := q.EnqueueEmail(ctx, sqlc.EnqueueEmailParams{
		ManagerID: tenantID, EventKey: eventKey, Recipient: recipient, Template: template, Payload: raw,
	})
	return n > 0, err
}

// ClaimDue leases up to batch due emails for this worker.
func (m OutboxModel) ClaimDue(ctx context.Context, batch int) ([]OutboxEmail, error) {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	rows, err := m.AdminStore.ClaimDueEmails(ctx, sqlc.ClaimDueEmailsParams{
		LeaseSeconds: int32(OutboxLease / time.Second), Batch: int32(batch),
	})
	if err != nil {
		return nil, err
	}
	out := make([]OutboxEmail, 0, len(rows))
	for _, r := range rows {
		var payload map[string]any
		if err := json.Unmarshal(r.Payload, &payload); err != nil {
			payload = map[string]any{} // a corrupt payload fails at render, and is retried to 'failed' like any send error
		}
		out = append(out, OutboxEmail{
			ID: r.ID, ManagerID: r.ManagerID, Recipient: r.Recipient, Template: r.Template,
			Payload: payload, Attempts: int(r.Attempts),
		})
	}
	return out, nil
}

// MarkSent settles a delivered email.
func (m OutboxModel) MarkSent(ctx context.Context, id uuid.UUID) error {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()
	return m.AdminStore.MarkEmailSent(ctx, id)
}

// MarkFailed records a failed delivery of attempt number attempt: the row is
// retried later, or left 'failed' once OutboxMaxAttempts is reached.
func (m OutboxModel) MarkFailed(ctx context.Context, id uuid.UUID, attempt int, sendErr error) error {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	msg := sendErr.Error()
	if len(msg) > 500 {
		msg = msg[:500]
	}
	return m.AdminStore.MarkEmailFailed(ctx, sqlc.MarkEmailFailedParams{
		ID: id, LastError: &msg, MaxAttempts: OutboxMaxAttempts,
		RetrySeconds: int32(OutboxRetryDelay(attempt) / time.Second),
	})
}

// Stats summarises the queue for the System page.
func (m OutboxModel) Stats(ctx context.Context) (*OutboxStats, error) {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	row, err := m.AdminStore.OutboxStats(ctx)
	if err != nil {
		return nil, err
	}
	return &OutboxStats{
		Pending: int(row.Pending), Failed: int(row.Failed), Sent24h: int(row.Sent24h),
		OldestPendingAgeSeconds: int(row.OldestPendingAgeSeconds), LastError: row.LastError,
	}, nil
}

// OwnerAlert is one security event to tell the firm's owner about (D5, C3.7).
type OwnerAlert struct {
	// Key makes the alert idempotent: the same Key for the same firm queues
	// once. Use something unique to the event (an entry id, or the request id
	// plus what changed), never a bare transition that can legitimately repeat.
	Key string
	// Headline names the event; it is the email subject.
	Headline string
	// Target says what it was done to, including the amount (moneyfmt) for a
	// reversal. No secrets, and phone numbers masked (mailer.MaskPhone).
	Target string
	// Link is the absolute URL of the record, or "".
	Link string
}

// EnqueueOwnerAlert queues an alert to the firm login's email. The actor is
// the staff member (name and role) when staff is set, otherwise the firm
// login; ip is the request's resolved client address. The time is stamped now,
// in Africa/Nairobi. Queued (not sent): the outbox worker delivers it.
func (m OutboxModel) EnqueueOwnerAlert(ctx context.Context, manager *Manager, staff *Staff, ip string, a OwnerAlert) (bool, error) {
	recipient, payload, err := ownerAlertEmail(manager, staff, ip, a)
	if err != nil {
		return false, err
	}
	return m.Enqueue(ctx, manager.ID, a.Key, recipient, mailer.SecurityAlertTemplate, payload)
}

// EnqueueOwnerAlertIn is EnqueueOwnerAlert inside the transaction the caller
// already holds (q from Store.ExecTenantTx for manager.ID): the alert commits
// with the action that raised it, or not at all (C4.0, the transactional
// outbox). Platform actions use it through a TxHook.
func EnqueueOwnerAlertIn(ctx context.Context, q sqlc.Querier, manager *Manager, staff *Staff, ip string, a OwnerAlert) (bool, error) {
	recipient, payload, err := ownerAlertEmail(manager, staff, ip, a)
	if err != nil {
		return false, err
	}
	return EnqueueIn(ctx, q, manager.ID, a.Key, recipient, mailer.SecurityAlertTemplate, payload)
}

func ownerAlertEmail(manager *Manager, staff *Staff, ip string, a OwnerAlert) (recipient string, payload map[string]any, err error) {
	if manager == nil || manager.IsAnonymous() || manager.Email == "" {
		return "", nil, errors.New("outbox: no firm login to alert")
	}
	return manager.Email, mailer.SecurityAlertData(manager.FirmName, mailer.SecurityAlert{
		Headline: a.Headline, Actor: OwnerAlertActor(manager, staff), IP: ip, Target: a.Target, At: moneyfmt.Now(), Link: a.Link,
	}), nil
}

// OwnerAlertActor names who did it in an owner alert: the staff member (name
// and role) when staff is set, otherwise the firm login. Product APIs that
// capture an alert for the relay (rentals alert_outbox) use it too, so the
// email reads the same whichever path queued it.
func OwnerAlertActor(manager *Manager, staff *Staff) string {
	if staff != nil && !staff.IsAnonymous() {
		return staff.Name + " (" + staff.Role + ")"
	}
	return manager.FirmName + " (firm login)"
}

// RelayedAlert is an owner alert that was committed in a product database's
// own alert_outbox (same transaction as the action) and is now being moved
// here. Its fields were captured when the action happened, so the email names
// the real actor and time, not the relay's.
type RelayedAlert struct {
	Key       string    `json:"key"`
	Recipient string    `json:"recipient"`
	FirmName  string    `json:"firm_name"`
	Actor     string    `json:"actor"`
	IP        string    `json:"ip"`
	Headline  string    `json:"headline"`
	Target    string    `json:"target"`
	Link      string    `json:"link"`
	At        time.Time `json:"at"`
}

// EnqueueRelayed queues a relayed alert. Idempotent per (tenantID, Key): a
// relay that crashed after this call and runs again queues nothing new, so the
// owner gets one email.
func (m OutboxModel) EnqueueRelayed(ctx context.Context, tenantID uuid.UUID, a RelayedAlert) (bool, error) {
	payload := mailer.SecurityAlertData(a.FirmName, mailer.SecurityAlert{
		Headline: a.Headline, Actor: a.Actor, IP: a.IP, Target: a.Target, At: a.At.In(moneyfmt.Nairobi), Link: a.Link,
	})
	return m.Enqueue(ctx, tenantID, a.Key, a.Recipient, mailer.SecurityAlertTemplate, payload)
}
