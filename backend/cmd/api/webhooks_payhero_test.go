package main

import (
	"database/sql"
	"net/http"
	"strings"
	"testing"

	"github.com/codercollo/willcoll/backend/internal/db/sqlc"
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

func TestCollectionsWebhookAppliesAPaymentAndTextsTheTenant(t *testing.T) {
	p := newPayTestApp(t)
	p.app.config.payhero.collectionsChannelID = "CH-1"
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
	p.store.EXPECT().ResolveIntentByReference(gomock.Any(), gomock.Any()).Return(sqlc.ResolveIntentByReferenceRow{}, sql.ErrNoRows)
	p.store.EXPECT().ResolveChannelTenants(gomock.Any(), gomock.Any()).Return(nil, nil) // no such channel
	w := do(p.app.payheroCollectionsWebhookHandler, "POST", collectionsPath+"?token="+testWebhookSecret+"&channel_id=UNKNOWN", webhookBody("RCPT-LOST", "100", "X"), nil, nil)
	p.app.wg.Wait()
	// PayHero must not keep retrying something we can never place.
	if w.Code != http.StatusOK {
		t.Errorf("status %d, want 200", w.Code)
	}
}

func TestSubscriptionsWebhook(t *testing.T) {
	invoice := uuid.New()
	target := "/v1/webhooks/payhero/subscriptions?token=" + testWebhookSecret

	t.Run("settles the invoice and activates the subscription", func(t *testing.T) {
		p := newPayTestApp(t)
		p.store.EXPECT().ResolveSubscriptionInvoice(gomock.Any(), invoice).Return(sqlc.ResolveSubscriptionInvoiceRow{ID: invoice, ManagerID: testTenantID}, nil)
		p.q.EXPECT().GetSubscriptionInvoice(gomock.Any(), gomock.Any()).Return(sqlc.SubscriptionInvoice{ID: invoice, Amount: money("2500"), Status: "pending"}, nil)
		p.q.EXPECT().MarkSubscriptionInvoicePaid(gomock.Any(), gomock.Any()).Return(int64(1), nil)
		p.q.EXPECT().GetSubscriptionByManager(gomock.Any(), gomock.Any()).Return(sqlc.Subscription{ID: uuid.New(), Status: "past_due"}, nil)
		p.q.EXPECT().GetSubscriptionPlan(gomock.Any(), gomock.Any()).Return(sqlc.SubscriptionPlan{Price: money("2500"), BillingInterval: "monthly"}, nil)
		p.q.EXPECT().ActivateSubscription(gomock.Any(), gomock.Any()).Return(int32(2), nil)

		body := `{"response":{"Amount":2500,"ExternalReference":"` + invoice.String() + `","MpesaReceiptNumber":"SUB1","ResultCode":0}}`
		w := do(p.app.payheroSubscriptionsWebhookHandler, "POST", target, body, nil, nil)
		p.app.wg.Wait()
		if w.Code != http.StatusOK {
			t.Fatalf("status %d", w.Code)
		}
	})
	t.Run("activates the plan chosen on the invoice, not the subscription's current one", func(t *testing.T) {
		p := newPayTestApp(t)
		currentPlanID, chosenPlanID := uuid.New(), uuid.New()
		subID := uuid.New()
		p.store.EXPECT().ResolveSubscriptionInvoice(gomock.Any(), invoice).Return(sqlc.ResolveSubscriptionInvoiceRow{ID: invoice, ManagerID: testTenantID}, nil)
		p.q.EXPECT().GetSubscriptionInvoice(gomock.Any(), gomock.Any()).Return(sqlc.SubscriptionInvoice{ID: invoice, PlanID: chosenPlanID, Amount: money("3500"), Status: "pending"}, nil)
		p.q.EXPECT().MarkSubscriptionInvoicePaid(gomock.Any(), gomock.Any()).Return(int64(1), nil)
		p.q.EXPECT().GetSubscriptionByManager(gomock.Any(), gomock.Any()).Return(sqlc.Subscription{ID: subID, PlanID: currentPlanID, Status: "active"}, nil)
		p.q.EXPECT().GetSubscriptionPlan(gomock.Any(), chosenPlanID).Return(sqlc.SubscriptionPlan{ID: chosenPlanID, Price: money("3500"), BillingInterval: "monthly"}, nil)
		p.q.EXPECT().ActivateSubscription(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ any, a sqlc.ActivateSubscriptionParams) (int32, error) {
				if a.PlanID != chosenPlanID {
					t.Errorf("activated plan = %s, want the invoice's chosen plan %s", a.PlanID, chosenPlanID)
				}
				return 2, nil
			})

		body := `{"response":{"Amount":3500,"ExternalReference":"` + invoice.String() + `","MpesaReceiptNumber":"SUB2","ResultCode":0}}`
		w := do(p.app.payheroSubscriptionsWebhookHandler, "POST", target, body, nil, nil)
		p.app.wg.Wait()
		if w.Code != http.StatusOK {
			t.Fatalf("status %d", w.Code)
		}
	})
	t.Run("unauthorized callbacks are refused before anything is read", func(t *testing.T) {
		p := newPayTestApp(t)
		w := do(p.app.payheroSubscriptionsWebhookHandler, "POST", "/v1/webhooks/payhero/subscriptions", `{}`, nil, nil)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("status %d", w.Code)
		}
	})
	t.Run("a callback with no invoice reference is a 400", func(t *testing.T) {
		p := newPayTestApp(t)
		w := do(p.app.payheroSubscriptionsWebhookHandler, "POST", target, `{"response":{"Amount":1,"MpesaReceiptNumber":"X"}}`, nil, nil)
		if w.Code != http.StatusBadRequest {
			t.Errorf("status %d", w.Code)
		}
	})
}
