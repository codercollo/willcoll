package data

import (
	"context"
	"fmt"

	"github.com/codercollo/willcoll/backend/internal/payhero"
	"github.com/codercollo/willcoll/backend/pkg/moneyfmt"
)

// ProcessCollectionsPayment is the collections pipeline (system-design.txt
// 4.8): given a parsed PayHero callback it either records a failed STK push
// against its intent, or ingests the payment: dedupe on the M-Pesa receipt,
// match by intent (Track A) or by phone, name and open balances (Track B),
// and allocate, all in one serializable transaction. The returned result says
// whether a confirmation SMS is due (Applied).
//
// payload.ChannelID must be set for an organic paybill payment, which has no
// intent to identify the manager; the handler fills it from the request.
func (m Models) ProcessCollectionsPayment(ctx context.Context, payload *payhero.CollectionsWebhookPayload) (*IngestResult, error) {
	if !payload.Success {
		reason := payload.ResultDescription
		if reason == "" {
			reason = "payment was not completed"
		}
		if payload.AccountReference == "" {
			return &IngestResult{}, nil
		}
		return &IngestResult{}, m.Collections.FailIntent(ctx, payload.AccountReference, reason)
	}

	amount, err := moneyfmt.Parse(payload.Amount)
	if err != nil {
		return nil, fmt.Errorf("%w: amount %q", ErrInvalidInput, payload.Amount)
	}
	return m.Collections.Process(ctx, CollectionPayment{
		MpesaReceipt:     payload.MpesaReceipt,
		Amount:           amount,
		Msisdn:           payload.Msisdn,
		PayerName:        payload.PayerName,
		AccountReference: payload.AccountReference,
		ChannelID:        payload.ChannelID,
		PayheroReference: payload.CheckoutRequestID,
		Raw:              payload.Raw,
	})
}

// ProcessSubscriptionPayment is the platform-billing pipeline: it settles the
// manager subscription invoice named by the callback and flips the
// subscription to active. See BillingModel.ProcessSubscriptionPayment.
func (m Models) ProcessSubscriptionPayment(ctx context.Context, payload *payhero.SubscriptionsWebhookPayload) (*SubscriptionResult, error) {
	return m.Billing.ProcessSubscriptionPayment(ctx, payload)
}
