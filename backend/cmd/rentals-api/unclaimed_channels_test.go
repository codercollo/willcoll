package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	platformsqlc "github.com/codercollo/willcoll/backend/internal/platform/db/sqlc"
	"github.com/codercollo/willcoll/backend/internal/rentals/db"
	"github.com/codercollo/willcoll/backend/internal/rentals/db/sqlc"
	"github.com/google/uuid"
	"go.uber.org/mock/gomock"
)

func TestUnclaimedChannelTracker(t *testing.T) {
	var tr unclaimedChannelTracker
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	if got := tr.list(now); got == nil || len(got) != 0 {
		t.Fatalf("empty list = %#v, want empty non-nil slice", got)
	}
	tr.record(now.Add(-time.Hour), "CH-A", "", "")
	tr.record(now.Add(-30*time.Minute), "CH-A", "pid", "Runda")
	tr.record(now.Add(-10*time.Minute), "CH-B", "", "")
	got := tr.list(now)
	if len(got) != 2 || got[0].ChannelID != "CH-B" || got[1].ChannelID != "CH-A" {
		t.Fatalf("list = %+v, want CH-B then CH-A (most recent first)", got)
	}
	if got[1].Count != 2 || got[1].PropertyName != "Runda" {
		t.Errorf("CH-A = %+v, want count 2 and property Runda", got[1])
	}
	// Entries older than 24h drop out.
	if got := tr.list(now.Add(25 * time.Hour)); len(got) != 0 {
		t.Errorf("after 25h list = %+v, want empty", got)
	}
}

// A webhook for a channel with no platform claim is NOT posted (C3.2): nothing
// reaches the inbox insert or the local pipeline (gomock fails on any
// unexpected call), and it is raised as an unclaimed channel with its
// property.
func TestCollectionsWebhookUnclaimedChannelIsNotPostedAndAlarms(t *testing.T) {
	p := newPayTestApp(t)
	p.channelClaim = func(string) (platformsqlc.PaymentChannel, error) { return platformsqlc.PaymentChannel{}, sql.ErrNoRows }
	propID := uuid.New()
	p.store.EXPECT().ResolveChannelProperties(gomock.Any(), gomock.Any()).
		Return([]sqlc.ResolveChannelPropertiesRow{{ID: propID, TenantID: testTenantID, Name: "Runda Demo"}}, nil)
	var recorded []platformsqlc.RecordUnclaimedReceiptParams
	p.pStore.EXPECT().RecordUnclaimedReceipt(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, a platformsqlc.RecordUnclaimedReceiptParams) (int64, error) {
			recorded = append(recorded, a)
			return 1, nil
		})

	w := do(p.app.payheroCollectionsWebhookHandler, "POST", collectionsPath+"?token="+testWebhookSecret+"&channel_id=PH-CH-RUNDA-DEMO",
		webhookBody("RCPT-UNCLAIMED", "100", "X"), nil, nil)
	p.app.wg.Wait()
	if w.Code != http.StatusOK {
		t.Fatalf("status %d, want 200", w.Code)
	}
	// The money is on record, not just in a log line.
	if len(recorded) != 1 || recorded[0].MpesaReceipt != "RCPT-UNCLAIMED" || recorded[0].ChannelID != "PH-CH-RUNDA-DEMO" ||
		recorded[0].Product != "rentals" || recorded[0].Amount.Cents() != 10000 || len(recorded[0].RawPayload) == 0 {
		t.Fatalf("recorded unclaimed receipts = %+v, want one for RCPT-UNCLAIMED with the full payload", recorded)
	}
	if n := p.app.unclaimedRecordFailures.count24h(time.Now()); n != 0 {
		t.Errorf("unclaimedRecordFailures = %d, want 0", n)
	}
	got := p.app.unclaimedChannels.list(time.Now())
	if len(got) != 1 || got[0].ChannelID != "PH-CH-RUNDA-DEMO" || got[0].PropertyName != "Runda Demo" || got[0].PropertyID != propID.String() {
		t.Fatalf("unclaimed channels = %+v", got)
	}
	if n := p.app.inboxFallbacks.count24h(time.Now()); n != 0 {
		t.Errorf("inboxFallbacks = %d, want 0 (an unclaimed channel is refused, not a fallback)", n)
	}
}

// If platform cannot store the unclaimed receipt, the write is retried, the
// failure is counted for the health page, and the payment is still not posted.
func TestCollectionsWebhookUnclaimedRecordFailureIsCounted(t *testing.T) {
	p := newPayTestApp(t)
	p.channelClaim = func(string) (platformsqlc.PaymentChannel, error) { return platformsqlc.PaymentChannel{}, sql.ErrNoRows }
	p.store.EXPECT().ResolveChannelProperties(gomock.Any(), gomock.Any()).Return(nil, nil)
	p.pStore.EXPECT().RecordUnclaimedReceipt(gomock.Any(), gomock.Any()).Return(int64(0), errors.New("platform down")).Times(webhookAttempts)

	w := do(p.app.payheroCollectionsWebhookHandler, "POST", collectionsPath+"?token="+testWebhookSecret+"&channel_id=PH-CH-LOST",
		webhookBody("RCPT-LOST", "100", "X"), nil, nil)
	p.app.wg.Wait()
	if w.Code != http.StatusOK {
		t.Fatalf("status %d, want 200", w.Code)
	}
	if n := p.app.unclaimedRecordFailures.count24h(time.Now()); n != 1 {
		t.Errorf("unclaimedRecordFailures = %d, want 1", n)
	}
}

// The System page reads unclaimed_channels from rentals-api's health report.
func TestAdminSystemHealthReportsUnclaimedChannels(t *testing.T) {
	p := newPayTestApp(t)
	p.store.EXPECT().DatabaseHealth(gomock.Any()).Return(db.DatabaseHealth{}, nil)
	p.store.EXPECT().GetSystemMetadata(gomock.Any(), "last_backup").Return(sqlc.SystemMetadatum{}, sql.ErrNoRows)
	p.store.EXPECT().AlertOutboxStats(gomock.Any()).Return(sqlc.AlertOutboxStatsRow{}, nil)
	p.app.unclaimedChannels.record(time.Now(), "CH-X", "", "")

	w := do(p.app.adminSystemHealthHandler, "GET", "/v1/admin/system/health", "", nil, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	var body struct {
		Unclaimed []unclaimedChannel `json:"unclaimed_channels"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Unclaimed) != 1 || body.Unclaimed[0].ChannelID != "CH-X" {
		t.Errorf("unclaimed_channels = %+v", body.Unclaimed)
	}
}

// HEALTH_TIMEOUT, not the short per-query timeout, bounds the health checks.
func TestAdminSystemHealthUsesHealthTimeout(t *testing.T) {
	p := newPayTestApp(t)
	p.app.models.Platform.Timeout = time.Millisecond
	p.app.models.Platform.HealthTimeout = 5 * time.Second
	p.store.EXPECT().DatabaseHealth(gomock.Any()).DoAndReturn(func(ctx context.Context) (db.DatabaseHealth, error) {
		dl, ok := ctx.Deadline()
		if !ok || time.Until(dl) < time.Second {
			return db.DatabaseHealth{}, errors.New("deadline too short: HEALTH_TIMEOUT not applied")
		}
		return db.DatabaseHealth{}, nil
	})
	p.store.EXPECT().GetSystemMetadata(gomock.Any(), "last_backup").Return(sqlc.SystemMetadatum{}, sql.ErrNoRows)
	p.store.EXPECT().AlertOutboxStats(gomock.Any()).Return(sqlc.AlertOutboxStatsRow{}, nil)

	w := do(p.app.adminSystemHealthHandler, "GET", "/v1/admin/system/health", "", nil, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
}
