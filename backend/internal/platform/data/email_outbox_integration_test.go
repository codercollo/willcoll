package data

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/codercollo/willcoll/backend/internal/platform/db/sqlc"
	"github.com/google/uuid"
)

// The owner-alert outbox against a real willcoll_platform (C3.7): an alert
// commits with the action that raised it or not at all, is queued once per
// event, is retried with backoff and ends up 'failed' where the System page
// can see it, and one firm's rows are invisible to another.

func newOutboxFirm(t *testing.T, models Models, appConn *sql.DB) *Manager {
	t.Helper()
	m := &Manager{FirmName: "Outbox Firm", Username: "u" + uuid.NewString()[:10], Email: uuid.NewString()[:8] + "@outbox.example.com", Phone: "+254700000000", Status: ManagerStatusPending}
	if err := m.Password.Set("correct-horse-battery"); err != nil {
		t.Fatal(err)
	}
	if err := models.Managers.Insert(context.Background(), m); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = appConn.Exec(`DELETE FROM managers WHERE id = $1`, m.ID) }) // cascades to email_outbox
	return m
}

func outboxRows(t *testing.T, adminConn *sql.DB, firm uuid.UUID) (total int) {
	t.Helper()
	if err := adminConn.QueryRow(`SELECT count(*) FROM email_outbox WHERE manager_id = $1`, firm).Scan(&total); err != nil {
		t.Fatal(err)
	}
	return total
}

// claimMine leases due emails and returns only this firm's, so rows other
// tests left behind never matter.
func claimMine(t *testing.T, models Models, firm uuid.UUID) []OutboxEmail {
	t.Helper()
	all, err := models.Outbox.ClaimDue(context.Background(), 1000)
	if err != nil {
		t.Fatal(err)
	}
	var mine []OutboxEmail
	for _, e := range all {
		if e.ManagerID == firm {
			mine = append(mine, e)
		}
	}
	return mine
}

func TestOutboxEnqueueIsAtomicWithTheAction(t *testing.T) {
	models, appConn, adminConn := openPlatformModels(t)
	ctx := context.Background()
	firm := newOutboxFirm(t, models, appConn)
	payload := map[string]any{"headline": "A staff member's role was changed"}

	// The action fails after the alert was queued: the transaction rolls back and
	// no alert survives for something that did not happen.
	boom := errors.New("the action failed")
	err := models.Outbox.Store.ExecTenantTx(ctx, firm.ID, func(q sqlc.Querier) error {
		if queued, err := EnqueueIn(ctx, q, firm.ID, "role:rolled-back", firm.Email, "security_alert", payload); err != nil || !queued {
			t.Errorf("EnqueueIn in the transaction: queued=%v err=%v", queued, err)
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("transaction err = %v, want the action's error", err)
	}
	if n := outboxRows(t, adminConn, firm.ID); n != 0 {
		t.Fatalf("a rolled-back action left %d outbox rows, want 0", n)
	}

	// The action commits: the alert is there, pending, with nothing sent yet.
	err = models.Outbox.Store.ExecTenantTx(ctx, firm.ID, func(q sqlc.Querier) error {
		_, err := EnqueueIn(ctx, q, firm.ID, "role:committed", firm.Email, "security_alert", payload)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	var status string
	var attempts int
	if err := adminConn.QueryRow(`SELECT status, attempts FROM email_outbox WHERE manager_id = $1 AND event_key = 'role:committed'`, firm.ID).Scan(&status, &attempts); err != nil {
		t.Fatal(err)
	}
	if status != OutboxPending || attempts != 0 {
		t.Errorf("committed alert = %s/%d attempts, want pending/0", status, attempts)
	}
}

func TestOutboxEnqueueIsIdempotentPerEvent(t *testing.T) {
	models, appConn, adminConn := openPlatformModels(t)
	ctx := context.Background()
	firm := newOutboxFirm(t, models, appConn)
	other := newOutboxFirm(t, models, appConn)
	payload := map[string]any{"headline": "A payment channel was changed"}

	// The same event, delivered by 20 racing handlers, is queued once.
	const racers = 20
	var wg sync.WaitGroup
	var mu sync.Mutex
	queuedCount := 0
	for range racers {
		wg.Go(func() {
			queued, err := models.Outbox.Enqueue(ctx, firm.ID, "req-1:channel.updated:prop-1", firm.Email, "security_alert", payload)
			if err != nil {
				t.Errorf("Enqueue: %v", err)
				return
			}
			if queued {
				mu.Lock()
				queuedCount++
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	if queuedCount != 1 || outboxRows(t, adminConn, firm.ID) != 1 {
		t.Fatalf("%d racing enqueues of one event: %d reported queued, %d rows; want 1 and 1", racers, queuedCount, outboxRows(t, adminConn, firm.ID))
	}

	// A different event is its own alert; the same key for another firm is not a duplicate.
	if queued, err := models.Outbox.Enqueue(ctx, firm.ID, "req-2:channel.updated:prop-1", firm.Email, "security_alert", payload); err != nil || !queued {
		t.Errorf("a second event: queued=%v err=%v", queued, err)
	}
	if queued, err := models.Outbox.Enqueue(ctx, other.ID, "req-1:channel.updated:prop-1", other.Email, "security_alert", payload); err != nil || !queued {
		t.Errorf("the same key for another firm: queued=%v err=%v", queued, err)
	}
	if outboxRows(t, adminConn, firm.ID) != 2 || outboxRows(t, adminConn, other.ID) != 1 {
		t.Errorf("rows = %d and %d, want 2 and 1", outboxRows(t, adminConn, firm.ID), outboxRows(t, adminConn, other.ID))
	}

	// An incomplete alert is refused rather than queued half-formed.
	for _, c := range []struct{ key, to, tmpl string }{{"", firm.Email, "t"}, {"k", "", "t"}, {"k", firm.Email, ""}} {
		if _, err := models.Outbox.Enqueue(ctx, firm.ID, c.key, c.to, c.tmpl, payload); err == nil {
			t.Errorf("Enqueue(%q, %q, %q) succeeded, want an error", c.key, c.to, c.tmpl)
		}
	}

	// Row level security: another firm's transaction cannot queue mail for this one.
	err := models.Outbox.Store.ExecTenantTx(ctx, other.ID, func(q sqlc.Querier) error {
		_, err := q.EnqueueEmail(ctx, sqlc.EnqueueEmailParams{ManagerID: firm.ID, EventKey: "forged", Recipient: firm.Email, Template: "security_alert", Payload: []byte(`{}`)})
		return err
	})
	if err == nil {
		t.Error("a tenant transaction queued an email for a different firm")
	}
}

func TestOutboxRetriesThenFailsVisibly(t *testing.T) {
	models, appConn, adminConn := openPlatformModels(t)
	ctx := context.Background()
	firm := newOutboxFirm(t, models, appConn)
	before, err := models.Outbox.Stats(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := models.Outbox.Enqueue(ctx, firm.ID, "retry-me", firm.Email, "security_alert", map[string]any{"headline": "x"}); err != nil {
		t.Fatal(err)
	}
	makeDue := func() {
		t.Helper()
		if _, err := adminConn.Exec(`UPDATE email_outbox SET next_attempt_at = now() - interval '1 second' WHERE manager_id = $1`, firm.ID); err != nil {
			t.Fatal(err)
		}
	}

	// First attempt: claimed, so leased and invisible to a second worker.
	first := claimMine(t, models, firm.ID)
	if len(first) != 1 || first[0].Attempts != 1 {
		t.Fatalf("first claim = %+v, want one email on attempt 1", first)
	}
	if again := claimMine(t, models, firm.ID); len(again) != 0 {
		t.Fatalf("a second worker claimed a leased email: %+v", again)
	}

	// It fails: it stays pending, waits out the backoff, and keeps the reason.
	if err := models.Outbox.MarkFailed(ctx, first[0].ID, first[0].Attempts, errors.New("smtp: connection refused")); err != nil {
		t.Fatal(err)
	}
	var status string
	var lastError sql.NullString
	var wait float64
	if err := adminConn.QueryRow(`SELECT status, last_error, EXTRACT(EPOCH FROM next_attempt_at - now()) FROM email_outbox WHERE id = $1`, first[0].ID).Scan(&status, &lastError, &wait); err != nil {
		t.Fatal(err)
	}
	if status != OutboxPending || lastError.String != "smtp: connection refused" || wait < 20 || wait > 40 {
		t.Fatalf("after one failure: %s, error %q, next attempt in %.0fs; want pending, the error, ~30s", status, lastError.String, wait)
	}
	if early := claimMine(t, models, firm.ID); len(early) != 0 {
		t.Fatalf("an email was retried before its backoff elapsed: %+v", early)
	}

	// Once the backoff passes it is retried, and the attempt count climbs.
	for attempt := 2; attempt <= OutboxMaxAttempts; attempt++ {
		makeDue()
		got := claimMine(t, models, firm.ID)
		if len(got) != 1 || got[0].Attempts != attempt {
			t.Fatalf("attempt %d claim = %+v", attempt, got)
		}
		if err := models.Outbox.MarkFailed(ctx, got[0].ID, attempt, errors.New("smtp: connection refused")); err != nil {
			t.Fatal(err)
		}
	}

	// Out of attempts: 'failed', never claimed again, and visible to the System page.
	if err := adminConn.QueryRow(`SELECT status FROM email_outbox WHERE manager_id = $1`, firm.ID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != OutboxFailed {
		t.Fatalf("status after %d failures = %s, want failed", OutboxMaxAttempts, status)
	}
	makeDue()
	if got := claimMine(t, models, firm.ID); len(got) != 0 {
		t.Fatalf("a failed email was claimed again: %+v", got)
	}
	after, err := models.Outbox.Stats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if after.Failed != before.Failed+1 {
		t.Errorf("System page failed count %d -> %d, want +1", before.Failed, after.Failed)
	}
	if after.LastError == nil || !strings.Contains(*after.LastError, "smtp: connection refused") {
		t.Errorf("System page last_error = %v, want the reason for the failure", after.LastError)
	}
	var stored string
	if err := adminConn.QueryRow(`SELECT last_error FROM email_outbox WHERE manager_id = $1`, firm.ID).Scan(&stored); err != nil || stored != "smtp: connection refused" {
		t.Errorf("stored last_error = %q (err %v)", stored, err)
	}
}

func TestOutboxDeliveryAndRecoveryAfterACrash(t *testing.T) {
	models, appConn, adminConn := openPlatformModels(t)
	ctx := context.Background()
	firm := newOutboxFirm(t, models, appConn)
	before, err := models.Outbox.Stats(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := models.Outbox.Enqueue(ctx, firm.ID, "send-me", firm.Email, "security_alert", map[string]any{"headline": "x"}); err != nil {
		t.Fatal(err)
	}

	// A worker claims the email and dies before settling it: once the lease
	// lapses another worker sends it (at least once, never lost).
	if got := claimMine(t, models, firm.ID); len(got) != 1 {
		t.Fatalf("claim = %+v", got)
	}
	if _, err := adminConn.Exec(`UPDATE email_outbox SET next_attempt_at = now() - interval '1 second' WHERE manager_id = $1`, firm.ID); err != nil {
		t.Fatal(err)
	}
	got := claimMine(t, models, firm.ID)
	if len(got) != 1 || got[0].Attempts != 2 {
		t.Fatalf("recovery claim = %+v, want the same email on attempt 2", got)
	}
	if err := models.Outbox.MarkSent(ctx, got[0].ID); err != nil {
		t.Fatal(err)
	}

	var status string
	var sentAt sql.NullTime
	if err := adminConn.QueryRow(`SELECT status, sent_at FROM email_outbox WHERE id = $1`, got[0].ID).Scan(&status, &sentAt); err != nil {
		t.Fatal(err)
	}
	if status != OutboxSent || !sentAt.Valid || time.Since(sentAt.Time) > time.Minute {
		t.Errorf("after MarkSent: %s, sent_at %v", status, sentAt)
	}
	if again := claimMine(t, models, firm.ID); len(again) != 0 {
		t.Errorf("a sent email was claimed again: %+v", again)
	}
	after, err := models.Outbox.Stats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if after.Sent24h != before.Sent24h+1 {
		t.Errorf("sent_24h %d -> %d, want +1", before.Sent24h, after.Sent24h)
	}
}

// A staff change and its owner alert are one transaction (C4.0): a hook that
// fails after the alert was enqueued rolls the change back with it, and a
// change that commits has its alert, once.
func TestStaffChangeAndOwnerAlertAreAtomic(t *testing.T) {
	models, appConn, adminConn := openPlatformModels(t)
	ctx := context.Background()
	firm := newOutboxFirm(t, models, appConn)

	staff := &Staff{Name: "Atomic Officer", Email: uuid.NewString()[:8] + "@atomic.example.com", Role: RoleOfficer, ModuleAccess: []string{ProductRentals}}
	if err := models.Staff.Invite(ctx, firm.ID, staff); err != nil {
		t.Fatal(err)
	}
	alert := func(key string) TxHook {
		return func(ctx context.Context, q sqlc.Querier) error {
			_, err := EnqueueOwnerAlertIn(ctx, q, firm, nil, "198.51.100.7", OwnerAlert{Key: key, Headline: "A staff member's role was changed", Target: "x"})
			return err
		}
	}

	// The change is saved and the alert enqueued, then a later hook fails:
	// neither survives.
	boom := errors.New("a later step failed")
	changed := *staff
	changed.Role = RoleViewer
	err := models.Staff.Update(ctx, firm.ID, &changed, alert("role:rolled-back"), func(context.Context, sqlc.Querier) error { return boom })
	if !errors.Is(err, boom) {
		t.Fatalf("Update err = %v, want the hook's error", err)
	}
	if n := outboxRows(t, adminConn, firm.ID); n != 0 {
		t.Fatalf("a rolled-back staff change left %d alerts, want 0", n)
	}
	got, err := models.Staff.Get(ctx, firm.ID, staff.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Role != RoleOfficer {
		t.Fatalf("role after a rolled-back change = %q, want officer", got.Role)
	}

	// Committed: the change and exactly one alert.
	changed = *got
	changed.Role = RoleViewer
	if err := models.Staff.Update(ctx, firm.ID, &changed, alert("role:committed")); err != nil {
		t.Fatal(err)
	}
	if got, _ = models.Staff.Get(ctx, firm.ID, staff.ID); got.Role != RoleViewer {
		t.Errorf("role after a committed change = %q, want viewer", got.Role)
	}
	if n := outboxRows(t, adminConn, firm.ID); n != 1 {
		t.Errorf("a committed staff change queued %d alerts, want 1", n)
	}

	// A failed Invite leaves no alert either.
	invitee := &Staff{Name: "Never Invited", Email: uuid.NewString()[:8] + "@atomic.example.com", Role: RoleViewer, ModuleAccess: []string{ProductRentals}}
	if err := models.Staff.Invite(ctx, firm.ID, invitee, alert("invite:rolled-back"), func(context.Context, sqlc.Querier) error { return boom }); !errors.Is(err, boom) {
		t.Fatalf("Invite err = %v, want the hook's error", err)
	}
	if n := outboxRows(t, adminConn, firm.ID); n != 1 {
		t.Errorf("a rolled-back invite changed the alert count to %d, want still 1", n)
	}
}

// A relayed alert (committed in a product database's own alert_outbox) is
// queued once per event however many times the relay hands it over: the
// duplicate-relay guarantee (C4.0).
func TestEnqueueRelayedIsIdempotent(t *testing.T) {
	models, appConn, adminConn := openPlatformModels(t)
	ctx := context.Background()
	firm := newOutboxFirm(t, models, appConn)

	a := RelayedAlert{
		Key: "ledger_entry.reversed:" + uuid.NewString(), Recipient: firm.Email, FirmName: firm.FirmName,
		Actor: "Ann (accountant)", IP: "198.51.100.7", Headline: "A ledger entry was reversed",
		Target: "KES 12,000.00", Link: "https://rentals.example.com/units/1", At: time.Now().Add(-time.Hour),
	}
	for i := range 3 {
		queued, err := models.Outbox.EnqueueRelayed(ctx, firm.ID, a)
		if err != nil {
			t.Fatal(err)
		}
		if queued != (i == 0) {
			t.Errorf("relay #%d: queued=%v, want %v", i+1, queued, i == 0)
		}
	}
	if n := outboxRows(t, adminConn, firm.ID); n != 1 {
		t.Fatalf("3 relays of one alert queued %d emails, want 1", n)
	}
	mine := claimMine(t, models, firm.ID)
	if len(mine) != 1 {
		t.Fatalf("claimed %d emails, want 1", len(mine))
	}
	if got, _ := mine[0].Payload["Actor"].(string); got != "Ann (accountant)" {
		t.Errorf("relayed email actor = %q, want the original actor, not the relay's", got)
	}
}
