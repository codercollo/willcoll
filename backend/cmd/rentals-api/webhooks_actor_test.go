package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	platformsqlc "github.com/codercollo/willcoll/backend/internal/platform/db/sqlc"
	"github.com/codercollo/willcoll/backend/internal/rentals/db/sqlc"
	"github.com/google/uuid"
	"go.uber.org/mock/gomock"
)

// C3.2, the webhook actor. One test per rule in docs/security/c3-spec.md.

// A bad or missing signature is 401, is logged, and writes nothing: the
// harness stores have no expectations, so any inbox or ledger call fails the
// test.
func TestWebhookActorBadSignatureIs401LoggedAndWritesNothing(t *testing.T) {
	body := webhookBody("RCPT-SIG", "1000", "WC-1")
	mac := hmac.New(sha256.New, []byte(testWebhookSecret))
	mac.Write([]byte(body))
	good := hex.EncodeToString(mac.Sum(nil))
	bad := good[:len(good)-1] + "0"

	for name, headers := range map[string]map[string]string{
		"tampered signature": {"X-Payhero-Signature": bad},
		"no credential":      nil,
	} {
		t.Run(name, func(t *testing.T) {
			p := newPayTestApp(t)
			var logs bytes.Buffer
			p.app.logger = slog.New(slog.NewTextHandler(&logs, nil))

			w := do(p.app.payheroCollectionsWebhookHandler, "POST", collectionsPath, body, nil, headers)
			p.app.wg.Wait()
			if w.Code != http.StatusUnauthorized {
				t.Fatalf("status %d, want 401", w.Code)
			}
			if !strings.Contains(logs.String(), "payhero webhook refused") {
				t.Errorf("refusal was not logged: %q", logs.String())
			}
		})
	}

	// And the right signature is accepted, so the test above is not vacuous.
	p := newPayTestApp(t)
	expectInboxClaimed(p)
	expectOrganicRouting(p)
	p.q.EXPECT().CreatePayment(gomock.Any(), gomock.Any()).Return(sqlc.Payment{}, sql.ErrNoRows)
	w := do(p.app.payheroCollectionsWebhookHandler, "POST", collectionsPath+"?channel_id=CH-1", body, nil, map[string]string{"X-Payhero-Signature": good})
	p.app.wg.Wait()
	if w.Code != http.StatusOK {
		t.Fatalf("good signature: status %d, want 200", w.Code)
	}
}

// Replay: the same receipt delivered twice is posted once. The first delivery
// claims and posts; the second finds the inbox row 'posted' and stops before
// the local pipeline (no expectations for it). PayHero's callback carries no
// timestamp this code can read (see payhero.ParseCollectionsPayload), so
// idempotency on the receipt is the replay control.
func TestWebhookActorReplayOfAPostedReceiptIsIdempotent(t *testing.T) {
	p := newPayTestApp(t)
	p.pStore.EXPECT().ClaimPaymentInbox(gomock.Any(), gomock.Any()).Return(platformsqlc.PaymentInbox{}, nil)
	p.pStore.EXPECT().MarkPaymentInboxPosted(gomock.Any(), "RCPT-REPLAY").Return(int64(1), nil)
	expectOrganicRouting(p)
	p.q.EXPECT().CreatePayment(gomock.Any(), gomock.Any()).Return(sqlc.Payment{}, sql.ErrNoRows)
	path := collectionsPath + "?token=" + testWebhookSecret + "&channel_id=CH-1"
	do(p.app.payheroCollectionsWebhookHandler, "POST", path, webhookBody("RCPT-REPLAY", "100", "X"), nil, nil)
	p.app.wg.Wait()

	p.pStore.EXPECT().ClaimPaymentInbox(gomock.Any(), gomock.Any()).Return(platformsqlc.PaymentInbox{}, sql.ErrNoRows)
	p.pStore.EXPECT().GetPaymentInboxByReceipt(gomock.Any(), "RCPT-REPLAY").
		Return(platformsqlc.PaymentInbox{MpesaReceipt: "RCPT-REPLAY", Product: "rentals", Status: "posted"}, nil)
	w := do(p.app.payheroCollectionsWebhookHandler, "POST", path, webhookBody("RCPT-REPLAY", "100", "X"), nil, nil)
	p.app.wg.Wait()
	if w.Code != http.StatusOK {
		t.Errorf("replay status %d, want 200", w.Code)
	}
}

// Channel: a channel claimed by a different product is refused before the
// inbox insert or any posting, whatever the payload says.
func TestWebhookActorChannelClaimedByAnotherProductIsNotPosted(t *testing.T) {
	p := newPayTestApp(t)
	p.channelClaim = func(id string) (platformsqlc.PaymentChannel, error) {
		return platformsqlc.PaymentChannel{ChannelID: id, ManagerID: testTenantID, Product: "estates"}, nil
	}
	w := do(p.app.payheroCollectionsWebhookHandler, "POST", collectionsPath+"?token="+testWebhookSecret+"&channel_id=CH-ESTATES",
		webhookBody("RCPT-X", "100", "X"), nil, nil)
	p.app.wg.Wait()
	if w.Code != http.StatusOK {
		t.Errorf("status %d, want 200 (PayHero is still acked)", w.Code)
	}
}

// Channel: the firm comes from the claim. A payload whose reference points at
// an intent of firm B, delivered on a channel firm A claimed, is refused by the
// pipeline and nothing is written (no CreatePayment expectation).
func TestWebhookActorFirmComesFromTheClaimNotThePayload(t *testing.T) {
	p := newPayTestApp(t)
	otherFirm := uuid.New()
	p.pStore.EXPECT().ClaimPaymentInbox(gomock.Any(), gomock.Any()).Return(platformsqlc.PaymentInbox{}, nil)
	p.store.EXPECT().ResolveIntentByReference(gomock.Any(), gomock.Any()).
		Return(sqlc.ResolveIntentByReferenceRow{ID: uuid.New(), TenantID: otherFirm, UnitID: uuid.New()}, nil)

	w := do(p.app.payheroCollectionsWebhookHandler, "POST", collectionsPath+"?token="+testWebhookSecret+"&channel_id=CH-A",
		webhookBody("RCPT-CROSS", "100", "WC-OTHER-FIRM"), nil, nil)
	p.app.wg.Wait()
	if w.Code != http.StatusOK {
		t.Errorf("status %d, want 200", w.Code)
	}
}
