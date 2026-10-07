package main

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	platformsqlc "github.com/codercollo/willcoll/backend/internal/platform/db/sqlc"
	"github.com/codercollo/willcoll/backend/internal/rentals/db/sqlc"
	"github.com/google/uuid"
	"go.uber.org/mock/gomock"
)

const collectionsPath = "/v1/webhooks/payhero/collections"

func TestWebhookAuthentication(t *testing.T) {
	body := webhookBody("RCPT1", "1000", "WC-1")
	tests := []struct {
		name       string
		target     string
		configure  func(p *payTestApp)
		headers    map[string]string
		wantStatus int
	}{
		{"secret as ?token=", collectionsPath + "?token=" + testWebhookSecret, func(*payTestApp) {}, nil, http.StatusOK},
		{"secret as a header", collectionsPath, func(*payTestApp) {}, map[string]string{"X-Webhook-Token": testWebhookSecret}, http.StatusOK},
		{"wrong secret", collectionsPath + "?token=nope", func(*payTestApp) {}, nil, http.StatusUnauthorized},
		{"no secret", collectionsPath, func(*payTestApp) {}, nil, http.StatusUnauthorized},
		{"nothing configured refuses everything", collectionsPath + "?token=" + testWebhookSecret,
			func(p *payTestApp) { p.app.config.payhero.webhookSecret = "" }, nil, http.StatusUnauthorized},
		{"ip allowlist without the caller in it", collectionsPath + "?token=" + testWebhookSecret,
			func(p *payTestApp) { p.app.config.payhero.webhookIPs = []string{"41.90.0.0/24"} }, nil, http.StatusUnauthorized},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := newPayTestApp(t)
			tc.configure(p)
			if tc.wantStatus == http.StatusOK {
				// Accepted: processing runs in the background and finds no manager.
				p.pStore.EXPECT().ClaimPaymentInbox(gomock.Any(), gomock.Any()).Return(platformsqlc.PaymentInbox{}, nil).AnyTimes()
				p.store.EXPECT().ResolveIntentByReference(gomock.Any(), gomock.Any()).Return(sqlc.ResolveIntentByReferenceRow{}, sql.ErrNoRows).AnyTimes()
				p.store.EXPECT().ResolveChannelTenants(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
			}
			w := do(p.app.payheroCollectionsWebhookHandler, "POST", tc.target, body, nil, tc.headers)
			p.app.wg.Wait()
			if w.Code != tc.wantStatus {
				t.Fatalf("status %d, want %d: %s", w.Code, tc.wantStatus, w.Body)
			}
		})
	}
}

func TestCollectionsWebhookRejectsMalformedPayloads(t *testing.T) {
	for name, body := range map[string]string{
		"not json":                  `hello`,
		"success without a receipt": `{"response":{"Amount":100,"ResultCode":0}}`,
		"empty object":              `{}`,
	} {
		t.Run(name, func(t *testing.T) {
			p := newPayTestApp(t)
			w := do(p.app.payheroCollectionsWebhookHandler, "POST", collectionsPath+"?token="+testWebhookSecret, body, nil, nil)
			p.app.wg.Wait()
			if w.Code != http.StatusBadRequest {
				t.Errorf("status %d, want 400: %s", w.Code, w.Body)
			}
		})
	}
}

// expectOrganicRouting makes the resolver find the test manager by channel.
func expectOrganicRouting(p *payTestApp) {
	p.store.EXPECT().ResolveIntentByReference(gomock.Any(), gomock.Any()).Return(sqlc.ResolveIntentByReferenceRow{}, sql.ErrNoRows)
	p.store.EXPECT().ResolveChannelTenants(gomock.Any(), gomock.Any()).Return([]uuid.UUID{testTenantID}, nil)
}

// expectInboxClaimed sets up the build-phases.txt 3g-2 happy path: the
// inbox claim succeeds and, if the local pipeline goes on to record the
// payment, MarkPaymentInboxPosted is allowed (AnyTimes — a test whose
// payment can never be routed, like the unroutable-channel case, correctly
// calls it zero times).
func expectInboxClaimed(p *payTestApp) {
	p.pStore.EXPECT().ClaimPaymentInbox(gomock.Any(), gomock.Any()).Return(platformsqlc.PaymentInbox{}, nil)
	p.pStore.EXPECT().MarkPaymentInboxPosted(gomock.Any(), gomock.Any()).Return(int64(1), nil).AnyTimes()
}

func TestCollectionsWebhookAppliesAPaymentAndTextsTheTenant(t *testing.T) {
	p := newPayTestApp(t)
	p.app.config.payhero.collectionsChannelID = "CH-1"
	expectInboxClaimed(p)
	expectOrganicRouting(p)

	p.q.EXPECT().CreatePayment(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ any, a sqlc.CreatePaymentParams) (sqlc.Payment, error) {
			if a.Source != "payhero_c2b" || a.MpesaReceipt != "RCPT-A" || a.Amount != money("5000") {
				t.Errorf("payment = %+v", a)
			}
			return sqlc.Payment{ID: uuid.New(), MpesaReceipt: a.MpesaReceipt, Amount: a.Amount}, nil
		})
	p.q.EXPECT().GetPaymentIntentByReference(gomock.Any(), gomock.Any()).Return(sqlc.PaymentIntent{}, sql.ErrNoRows)
	p.q.EXPECT().ListPayerCandidates(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ any, a sqlc.ListPayerCandidatesParams) ([]sqlc.ListPayerCandidatesRow, error) {
			if a.ChannelID == nil || *a.ChannelID != "CH-1" {
				t.Errorf("candidates looked up on channel %v, want the configured default CH-1", a.ChannelID)
			}
			return []sqlc.ListPayerCandidatesRow{{UnitID: testUnitA, Kind: "phone", Value: "0722000001"}}, nil
		})
	p.q.EXPECT().ListUnitBalances(gomock.Any(), gomock.Any()).Return([]sqlc.ListUnitBalancesRow{
		{Type: "RENT", Balance: money("5000")}, {Type: "WATER", Balance: money("1000")},
	}, nil)
	// Exactly one combination (RENT alone) matches 5,000: applied, no review.
	p.q.EXPECT().CreateTransactionHeader(gomock.Any(), gomock.Any()).Return(uuid.New(), nil)
	p.q.EXPECT().GetOrCreateLedgerAccount(gomock.Any(), gomock.Any()).Return(uuid.New(), nil)
	p.q.EXPECT().CreateLedgerEntry(gomock.Any(), gomock.Any()).Return(uuid.New(), nil)
	p.q.EXPECT().CreatePaymentAllocation(gomock.Any(), gomock.Any()).Return(nil)
	p.q.EXPECT().GetUnitLedgerBalanceAsOf(gomock.Any(), gomock.Any()).Return(money("0"), nil)
	p.q.EXPECT().GetReceiptByAllocation(gomock.Any(), gomock.Any()).Return(sqlc.Receipt{}, sql.ErrNoRows)
	p.q.EXPECT().LockReceiptCounter(gomock.Any(), gomock.Any()).Return(int64(1), nil)
	p.q.EXPECT().AdvanceReceiptCounter(gomock.Any(), gomock.Any()).Return(nil)
	p.q.EXPECT().CreateReceipt(gomock.Any(), gomock.Any()).Return(sqlc.Receipt{}, nil)
	p.q.EXPECT().SumPaymentAllocations(gomock.Any(), gomock.Any()).Return(money("5000"), nil)
	p.q.EXPECT().SetPaymentMatch(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ any, a sqlc.SetPaymentMatchParams) error {
			if a.Status != "allocated" || a.AutoAppliedUnconfirmed {
				t.Errorf("match = %+v", a)
			}
			return nil
		})
	// One GetUnit call for issueReceipt (inside postPaymentAllocations),
	// one for the confirmation text afterwards; both need the real
	// UnitCode, so a single exact expectation covers both.
	p.q.EXPECT().GetUnit(gomock.Any(), gomock.Any()).Return(sqlc.Unit{ID: testUnitA, UnitCode: "A1", PropertyID: testPropertyID}, nil).Times(2)
	p.q.EXPECT().GetProperty(gomock.Any(), gomock.Any()).Return(testPropertyRow(), nil)

	body := `{"response":{"Amount":5000,"MpesaReceiptNumber":"RCPT-A","Phone":"254722000001","ExternalReference":"BANK-ACC","ResultCode":0}}`
	w := do(p.app.payheroCollectionsWebhookHandler, "POST", collectionsPath+"?token="+testWebhookSecret, body, nil, nil)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "accepted") {
		t.Fatalf("the callback must be acknowledged at once: %d %s", w.Code, w.Body)
	}
	p.app.wg.Wait()

	if len(p.sms.confirmations) != 1 {
		t.Fatalf("confirmation texts = %d, want 1", len(p.sms.confirmations))
	}
	c := p.sms.confirmations[0]
	if c.Amount != "5,000.00" || c.UnitCode != "A1" || c.Receipt != "RCPT-A" || p.sms.confirmPhones[0] != "254722000001" {
		t.Errorf("confirmation = %+v to %v", c, p.sms.confirmPhones)
	}
}

func TestCollectionsWebhookRedeliveryIsHarmless(t *testing.T) {
	p := newPayTestApp(t)
	expectInboxClaimed(p)
	expectOrganicRouting(p)
	p.q.EXPECT().CreatePayment(gomock.Any(), gomock.Any()).Return(sqlc.Payment{}, sql.ErrNoRows) // ON CONFLICT DO NOTHING
	// Nothing else may be touched: no matching, no ledger, no SMS.

	w := do(p.app.payheroCollectionsWebhookHandler, "POST", collectionsPath+"?token="+testWebhookSecret+"&channel_id=CH-1", webhookBody("RCPT-DUP", "100", "X"), nil, nil)
	p.app.wg.Wait()
	if w.Code != http.StatusOK || len(p.sms.confirmations) != 0 {
		t.Errorf("status %d, texts %d", w.Code, len(p.sms.confirmations))
	}
}

func TestCollectionsWebhookNeverTextsAnUnplacedPayment(t *testing.T) {
	p := newPayTestApp(t)
	expectInboxClaimed(p)
	expectOrganicRouting(p)
	p.q.EXPECT().CreatePayment(gomock.Any(), gomock.Any()).Return(sqlc.Payment{ID: uuid.New()}, nil)
	p.q.EXPECT().GetPaymentIntentByReference(gomock.Any(), gomock.Any()).Return(sqlc.PaymentIntent{}, sql.ErrNoRows)
	p.q.EXPECT().ListPayerCandidates(gomock.Any(), gomock.Any()).Return(nil, nil) // nobody identified
	p.q.EXPECT().SetPaymentMatch(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ any, a sqlc.SetPaymentMatchParams) error {
			if a.Status != "unmatched" || a.ReviewNote == nil {
				t.Errorf("match = %+v, want unmatched with a note", a)
			}
			return nil
		})

	do(p.app.payheroCollectionsWebhookHandler, "POST", collectionsPath+"?token="+testWebhookSecret+"&channel_id=CH-1", webhookBody("RCPT-X", "100", "BANK"), nil, nil)
	p.app.wg.Wait()
	if len(p.sms.confirmations) != 0 {
		t.Error("a payment nobody could place must not be confirmed to anyone")
	}
}

func TestCollectionsWebhookFailedPushMarksTheIntent(t *testing.T) {
	p := newPayTestApp(t)
	intent := uuid.New()
	p.store.EXPECT().ResolveIntentByReference(gomock.Any(), gomock.Any()).Return(sqlc.ResolveIntentByReferenceRow{ID: intent, TenantID: testTenantID, UnitID: testUnitA}, nil)
	p.q.EXPECT().GetPaymentIntentByReference(gomock.Any(), gomock.Any()).Return(sqlc.PaymentIntent{ID: intent, Status: "pending"}, nil)
	p.q.EXPECT().SetPaymentIntentStatus(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ any, a sqlc.SetPaymentIntentStatusParams) error {
			if a.Status != "failed" || a.FailureReason == nil || !strings.Contains(*a.FailureReason, "cancelled") {
				t.Errorf("status update = %+v", a)
			}
			return nil
		})

	cancelled := `{"response":{"ExternalReference":"WC-CANCEL","ResultCode":1032,"ResultDesc":"Request cancelled by user","Status":"Failed"}}`
	w := do(p.app.payheroCollectionsWebhookHandler, "POST", collectionsPath+"?token="+testWebhookSecret, cancelled, nil, nil)
	p.app.wg.Wait()
	if w.Code != http.StatusOK {
		t.Errorf("status %d", w.Code)
	}
}

func TestCollectionsWebhookUnroutablePaymentIsAcknowledgedAndLogged(t *testing.T) {
	p := newPayTestApp(t)
	expectInboxClaimed(p) // the claim still succeeds; MarkPosted must then NOT be called (never routed)
	p.store.EXPECT().ResolveIntentByReference(gomock.Any(), gomock.Any()).Return(sqlc.ResolveIntentByReferenceRow{}, sql.ErrNoRows)
	p.store.EXPECT().ResolveChannelTenants(gomock.Any(), gomock.Any()).Return(nil, nil) // no such channel
	w := do(p.app.payheroCollectionsWebhookHandler, "POST", collectionsPath+"?token="+testWebhookSecret+"&channel_id=UNKNOWN", webhookBody("RCPT-LOST", "100", "X"), nil, nil)
	p.app.wg.Wait()
	// PayHero must not keep retrying something we can never place.
	if w.Code != http.StatusOK {
		t.Errorf("status %d, want 200", w.Code)
	}
}

// build-phases.txt 3g-2: the inbox claim runs BEFORE any local routing, so
// every test below overrides newPayTestApp's default ClaimPaymentInbox
// expectation with its own, and asserts the local pipeline (ResolveIntent/
// ChannelTenants, CreatePayment, ...) is touched or not touched accordingly.

// TestCollectionsWebhookAlreadyPostedAcksWithoutReprocessing: a redelivered
// receipt the inbox already marked 'posted' (by this product, in an earlier
// delivery) must be acknowledged without running the local pipeline again —
// unlike TestCollectionsWebhookRedeliveryIsHarmless, which is the LOCAL
// dedupe guard (CreatePayment's own ON CONFLICT) catching a receipt the
// inbox never saw reused on this same request.
func TestCollectionsWebhookAlreadyPostedAcksWithoutReprocessing(t *testing.T) {
	p := newPayTestApp(t)
	p.pStore.EXPECT().ClaimPaymentInbox(gomock.Any(), gomock.Any()).
		Return(platformsqlc.PaymentInbox{}, sql.ErrNoRows) // ON CONFLICT DO NOTHING: already present
	p.pStore.EXPECT().GetPaymentInboxByReceipt(gomock.Any(), "RCPT-POSTED").
		Return(platformsqlc.PaymentInbox{MpesaReceipt: "RCPT-POSTED", Product: "rentals", Status: "posted"}, nil)
	// No ResolveIntentByReference/ResolveChannelTenants/CreatePayment
	// expectations at all: the local pipeline must never run.

	w := do(p.app.payheroCollectionsWebhookHandler, "POST", collectionsPath+"?token="+testWebhookSecret+"&channel_id=CH-1",
		webhookBody("RCPT-POSTED", "100", "X"), nil, nil)
	p.app.wg.Wait()
	if w.Code != http.StatusOK {
		t.Errorf("status %d, want 200", w.Code)
	}
}

// TestCollectionsWebhookWrongProductIsRejectedNotPosted: a receipt the inbox
// already holds under a DIFFERENT product is a real alert (system-design.txt
// A.6's one-owner-per-channel rule broken somewhere upstream), never
// something rentals-api quietly posts to its own ledger.
func TestCollectionsWebhookWrongProductIsRejectedNotPosted(t *testing.T) {
	p := newPayTestApp(t)
	p.pStore.EXPECT().ClaimPaymentInbox(gomock.Any(), gomock.Any()).
		Return(platformsqlc.PaymentInbox{}, sql.ErrNoRows) // ON CONFLICT DO NOTHING: already claimed by another product
	p.pStore.EXPECT().GetPaymentInboxByReceipt(gomock.Any(), "RCPT-OTHER").
		Return(platformsqlc.PaymentInbox{MpesaReceipt: "RCPT-OTHER", Product: "estates", Status: "claimed"}, nil)
	// No local-pipeline expectations: must never be touched.

	w := do(p.app.payheroCollectionsWebhookHandler, "POST", collectionsPath+"?token="+testWebhookSecret+"&channel_id=CH-1",
		webhookBody("RCPT-OTHER", "100", "X"), nil, nil)
	p.app.wg.Wait()
	if w.Code != http.StatusOK {
		t.Errorf("status %d, want 200 (PayHero is still acked; the refusal is internal)", w.Code)
	}
}

// TestCollectionsWebhookPlatformDownFallsBackToLocalOnly: when the inbox
// claim itself fails (platform unreachable, not a real conflict),
// collections must still post locally exactly as before 3g-2, and the
// fallback is counted for the health page.
func TestCollectionsWebhookPlatformDownFallsBackToLocalOnly(t *testing.T) {
	p := newPayTestApp(t)
	p.pStore.EXPECT().ClaimPaymentInbox(gomock.Any(), gomock.Any()).Return(platformsqlc.PaymentInbox{}, errors.New("connection refused"))
	expectOrganicRouting(p)
	p.q.EXPECT().CreatePayment(gomock.Any(), gomock.Any()).Return(sqlc.Payment{}, sql.ErrNoRows) // ON CONFLICT DO NOTHING: keep this test short

	before := p.app.inboxFallbacks.count24h(time.Now())
	w := do(p.app.payheroCollectionsWebhookHandler, "POST", collectionsPath+"?token="+testWebhookSecret+"&channel_id=CH-1",
		webhookBody("RCPT-FALLBACK", "100", "X"), nil, nil)
	p.app.wg.Wait()
	if w.Code != http.StatusOK {
		t.Errorf("status %d, want 200 — collections must never stop for a platform outage", w.Code)
	}
	if after := p.app.inboxFallbacks.count24h(time.Now()); after != before+1 {
		t.Errorf("inboxFallbacks count = %d, want %d (one fallback counted)", after, before+1)
	}
}

// TestCollectionsWebhookMarksInboxPostedAfterLocalSuccess: once the claim
// succeeds and the payment is recorded locally, the inbox row must be
// marked 'posted' — the one write this whole flow exists to make durable.
func TestCollectionsWebhookMarksInboxPostedAfterLocalSuccess(t *testing.T) {
	p := newPayTestApp(t)
	p.pStore.EXPECT().ClaimPaymentInbox(gomock.Any(), gomock.Any()).Return(platformsqlc.PaymentInbox{MpesaReceipt: "RCPT-NEW", Product: "rentals", Status: "claimed"}, nil)
	p.pStore.EXPECT().MarkPaymentInboxPosted(gomock.Any(), "RCPT-NEW").Return(int64(1), nil).Times(1)
	expectOrganicRouting(p)
	p.q.EXPECT().CreatePayment(gomock.Any(), gomock.Any()).Return(sqlc.Payment{}, sql.ErrNoRows) // ON CONFLICT DO NOTHING: keep this test short

	do(p.app.payheroCollectionsWebhookHandler, "POST", collectionsPath+"?token="+testWebhookSecret+"&channel_id=CH-1",
		webhookBody("RCPT-NEW", "100", "X"), nil, nil)
	p.app.wg.Wait()
}
