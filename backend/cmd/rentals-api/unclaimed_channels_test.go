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
	"github.com/jackc/pgx/v5/pgconn"
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

// A webhook for a channel with no platform claim (inbox FK 23503) is still
// posted locally, counted as a fallback, and raised as an unclaimed channel
// with its property.
func TestCollectionsWebhookUnclaimedChannelPostsLocallyAndAlarms(t *testing.T) {
	p := newPayTestApp(t)
	p.pStore.EXPECT().ClaimPaymentInbox(gomock.Any(), gomock.Any()).
		Return(platformsqlc.PaymentInbox{}, &pgconn.PgError{Code: "23503", ConstraintName: "payment_inbox_channel_id_fkey"})
	propID := uuid.New()
	p.store.EXPECT().ResolveChannelProperties(gomock.Any(), gomock.Any()).
		Return([]sqlc.ResolveChannelPropertiesRow{{ID: propID, TenantID: testTenantID, Name: "Runda Demo"}}, nil)
	expectOrganicRouting(p) // local posting still happens
	p.q.EXPECT().CreatePayment(gomock.Any(), gomock.Any()).Return(sqlc.Payment{}, sql.ErrNoRows)

	w := do(p.app.payheroCollectionsWebhookHandler, "POST", collectionsPath+"?token="+testWebhookSecret+"&channel_id=PH-CH-RUNDA-DEMO",
		webhookBody("RCPT-UNCLAIMED", "100", "X"), nil, nil)
	p.app.wg.Wait()
	if w.Code != http.StatusOK {
		t.Fatalf("status %d, want 200", w.Code)
	}
	got := p.app.unclaimedChannels.list(time.Now())
	if len(got) != 1 || got[0].ChannelID != "PH-CH-RUNDA-DEMO" || got[0].PropertyName != "Runda Demo" || got[0].PropertyID != propID.String() {
		t.Fatalf("unclaimed channels = %+v", got)
	}
	if n := p.app.inboxFallbacks.count24h(time.Now()); n != 1 {
		t.Errorf("inboxFallbacks = %d, want 1", n)
	}
}

// The System page reads unclaimed_channels from rentals-api's health report.
func TestAdminSystemHealthReportsUnclaimedChannels(t *testing.T) {
	p := newPayTestApp(t)
	p.store.EXPECT().DatabaseHealth(gomock.Any()).Return(db.DatabaseHealth{}, nil)
	p.store.EXPECT().GetSystemMetadata(gomock.Any(), "last_backup").Return(sqlc.SystemMetadatum{}, sql.ErrNoRows)
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

	w := do(p.app.adminSystemHealthHandler, "GET", "/v1/admin/system/health", "", nil, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
}
