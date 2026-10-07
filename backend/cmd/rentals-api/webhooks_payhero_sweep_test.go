package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"sync"
	"testing"
	"time"

	platformsqlc "github.com/codercollo/willcoll/backend/internal/platform/db/sqlc"
	"github.com/codercollo/willcoll/backend/internal/rentals/db/sqlc"
	"github.com/google/uuid"
	"go.uber.org/mock/gomock"
)

// build-phases.txt 3g-3: the sweeper re-drives a payment_inbox row still
// 'claimed' well after it should have posted — a process that crashed (or
// was killed) between claiming and posting locally.

// TestSweepStaleInboxPostsAndMarksACrashedClaim simulates exactly that
// crash: a row the sweeper finds 'claimed' with a stored raw payload, and
// nothing posted locally yet. The sweep must post it through the ordinary
// pipeline and mark it posted — the same outcome a fresh webhook delivery
// would have produced, just delayed.
func TestSweepStaleInboxPostsAndMarksACrashedClaim(t *testing.T) {
	p := newPayTestApp(t)
	claimedAt := time.Now().Add(-10 * time.Minute)
	raw := json.RawMessage(webhookBody("RCPT-CRASHED", "100", "BANK-ACC"))

	p.pStore.EXPECT().ListStaleClaimedPaymentInbox(gomock.Any(), gomock.Any()).
		Return([]platformsqlc.PaymentInbox{
			{MpesaReceipt: "RCPT-CRASHED", ChannelID: "CH-1", Product: "rentals", Status: "claimed", RawPayload: raw, ClaimedAt: claimedAt},
		}, nil)
	// The re-drive's own Claim call: still 'claimed' under the same
	// product, so it must continue to posting, exactly as a stale-but-not-
	// yet-posted row should.
	p.pStore.EXPECT().ClaimPaymentInbox(gomock.Any(), gomock.Any()).Return(platformsqlc.PaymentInbox{}, sql.ErrNoRows)
	p.pStore.EXPECT().GetPaymentInboxByReceipt(gomock.Any(), "RCPT-CRASHED").
		Return(platformsqlc.PaymentInbox{MpesaReceipt: "RCPT-CRASHED", Product: "rentals", Status: "claimed"}, nil)
	p.pStore.EXPECT().MarkPaymentInboxPosted(gomock.Any(), "RCPT-CRASHED").Return(int64(1), nil).Times(1)

	expectOrganicRouting(p)
	p.q.EXPECT().CreatePayment(gomock.Any(), gomock.Any()).Return(sqlc.Payment{}, sql.ErrNoRows) // keep this test short: ON CONFLICT DO NOTHING is still a successful post

	app := p.app
	app.sweepStaleInbox(context.Background())
}

// TestSweepStaleInboxUnparsablePayloadIsLoggedNotLost: a row whose stored
// raw payload can no longer be parsed is never silently dropped — it's
// logged and left 'claimed' for the next sweep (and for manual recovery),
// never posted with guessed data.
func TestSweepStaleInboxUnparsablePayloadIsLoggedNotLost(t *testing.T) {
	p := newPayTestApp(t)
	p.pStore.EXPECT().ListStaleClaimedPaymentInbox(gomock.Any(), gomock.Any()).
		Return([]platformsqlc.PaymentInbox{
			{MpesaReceipt: "RCPT-BAD", ChannelID: "CH-1", Product: "rentals", Status: "claimed", RawPayload: json.RawMessage(`not json`), ClaimedAt: time.Now()},
		}, nil)
	// No Claim/MarkPosted/local-pipeline expectations at all: an unparsable
	// payload must never reach any of them.

	p.app.sweepStaleInbox(context.Background())
}

// TestSweepStaleInboxEmptyListIsANoop: nothing stale means nothing happens.
func TestSweepStaleInboxEmptyListIsANoop(t *testing.T) {
	p := newPayTestApp(t)
	p.pStore.EXPECT().ListStaleClaimedPaymentInbox(gomock.Any(), gomock.Any()).Return(nil, nil)
	p.app.sweepStaleInbox(context.Background())
}

// TestCollectionsWebhookSameReceiptFiredTwiceConcurrentlyPostsOnce: two
// goroutines deliver the identical receipt at the same moment (PayHero's
// own retry racing a redelivery, or two load-balanced instances). Exactly
// one must actually post locally and mark the inbox row posted; the other
// must see the local pipeline's own dedupe (CreatePayment's ON CONFLICT)
// return a harmless duplicate.
func TestCollectionsWebhookSameReceiptFiredTwiceConcurrentlyPostsOnce(t *testing.T) {
	p := newPayTestApp(t)
	const receipt = "RCPT-RACE"

	// Both goroutines' Claim calls land on platform.payment_inbox, which
	// is the real serialization point in production (a single row's
	// unique constraint) — here, mocked as "both see it as freshly
	// claimable," which is the worst case for the local pipeline's own
	// guard to have to handle alone, and exactly what it's there for.
	p.pStore.EXPECT().ClaimPaymentInbox(gomock.Any(), gomock.Any()).Return(platformsqlc.PaymentInbox{}, nil).Times(2)
	p.pStore.EXPECT().MarkPaymentInboxPosted(gomock.Any(), receipt).Return(int64(1), nil).AnyTimes()

	p.store.EXPECT().ResolveIntentByReference(gomock.Any(), gomock.Any()).Return(sqlc.ResolveIntentByReferenceRow{}, sql.ErrNoRows).Times(2)
	p.store.EXPECT().ResolveChannelTenants(gomock.Any(), gomock.Any()).Return([]uuid.UUID{testTenantID}, nil).Times(2)

	var mu sync.Mutex
	posted := false
	p.q.EXPECT().CreatePayment(gomock.Any(), gomock.Any()).Times(2).DoAndReturn(
		func(_ any, a sqlc.CreatePaymentParams) (sqlc.Payment, error) {
			mu.Lock()
			defer mu.Unlock()
			if posted {
				return sqlc.Payment{}, sql.ErrNoRows // the local pipeline's own ON CONFLICT dedupe
			}
			posted = true
			return sqlc.Payment{ID: uuid.New(), MpesaReceipt: a.MpesaReceipt, Amount: a.Amount}, nil
		})
	p.q.EXPECT().GetPaymentIntentByReference(gomock.Any(), gomock.Any()).Return(sqlc.PaymentIntent{}, sql.ErrNoRows)
	p.q.EXPECT().ListPayerCandidates(gomock.Any(), gomock.Any()).Return(nil, nil)
	p.q.EXPECT().SetPaymentMatch(gomock.Any(), gomock.Any()).Return(nil)

	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w := do(p.app.payheroCollectionsWebhookHandler, http.MethodPost,
				collectionsPath+"?token="+testWebhookSecret+"&channel_id=CH-1", webhookBody(receipt, "100", "BANK"), nil, nil)
			if w.Code != http.StatusOK {
				t.Errorf("status %d, want 200", w.Code)
			}
		}()
	}
	wg.Wait()
	p.app.wg.Wait()
}
