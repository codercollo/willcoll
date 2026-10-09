package main

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/codercollo/willcoll/backend/internal/platform/db/sqlc"
	"github.com/google/uuid"
)

// fakeOutbox stands in for the email_outbox table behind the fake store:
// idempotent enqueue per (manager, event key), leased claims, retry/failed
// settling. Time is the fake's own clock so retries need no sleeping.
type fakeOutbox struct {
	mu   sync.Mutex
	now  time.Time
	rows []*sqlc.EmailOutbox
}

func newFakeOutbox() *fakeOutbox {
	return &fakeOutbox{now: time.Date(2026, 10, 9, 9, 0, 0, 0, time.UTC)}
}

func (o *fakeOutbox) advance(d time.Duration) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.now = o.now.Add(d)
}

func (o *fakeOutbox) enqueue(_ context.Context, a sqlc.EnqueueEmailParams) (int64, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	for _, r := range o.rows {
		if r.ManagerID == a.ManagerID && r.EventKey == a.EventKey {
			return 0, nil // ON CONFLICT DO NOTHING
		}
	}
	o.rows = append(o.rows, &sqlc.EmailOutbox{
		ID: uuid.New(), ManagerID: a.ManagerID, EventKey: a.EventKey, Recipient: a.Recipient, Template: a.Template,
		Payload: append(json.RawMessage(nil), a.Payload...), Status: "pending", NextAttemptAt: o.now, CreatedAt: o.now,
	})
	return 1, nil
}

func (o *fakeOutbox) claim(_ context.Context, a sqlc.ClaimDueEmailsParams) ([]sqlc.EmailOutbox, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	var out []sqlc.EmailOutbox
	for _, r := range o.rows {
		if len(out) >= int(a.Batch) {
			break
		}
		if r.Status == "pending" && !r.NextAttemptAt.After(o.now) {
			r.Attempts++
			r.NextAttemptAt = o.now.Add(time.Duration(a.LeaseSeconds) * time.Second)
			out = append(out, *r)
		}
	}
	return out, nil
}

func (o *fakeOutbox) markSent(_ context.Context, id uuid.UUID) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	for _, r := range o.rows {
		if r.ID == id {
			r.Status = "sent"
			sent := o.now
			r.SentAt = &sent
		}
	}
	return nil
}

func (o *fakeOutbox) markFailed(_ context.Context, a sqlc.MarkEmailFailedParams) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	for _, r := range o.rows {
		if r.ID == a.ID {
			r.LastError = a.LastError
			if r.Attempts >= a.MaxAttempts {
				r.Status = "dead"
			}
			r.NextAttemptAt = o.now.Add(time.Duration(a.RetrySeconds) * time.Second)
		}
	}
	return nil
}

func (o *fakeOutbox) stats(context.Context) (sqlc.OutboxStatsRow, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	var s sqlc.OutboxStatsRow
	for _, r := range o.rows {
		switch r.Status {
		case "pending":
			s.Pending++
			if age := o.now.Sub(r.CreatedAt).Seconds(); age > s.OldestPendingAgeSeconds {
				s.OldestPendingAgeSeconds = age
			}
		case "dead":
			s.Dead++
		case "sent":
			s.Sent24h++
		}
		if r.LastError != nil {
			s.LastError = r.LastError
		}
	}
	return s, nil
}

func (o *fakeOutbox) snapshot() []sqlc.EmailOutbox {
	o.mu.Lock()
	defer o.mu.Unlock()
	out := make([]sqlc.EmailOutbox, len(o.rows))
	for i, r := range o.rows {
		out[i] = *r
	}
	return out
}
