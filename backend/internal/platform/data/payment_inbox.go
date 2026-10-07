package data

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/codercollo/willcoll/backend/internal/platform/db"
	"github.com/codercollo/willcoll/backend/internal/platform/db/sqlc"
	"github.com/codercollo/willcoll/backend/pkg/moneyfmt"
	"github.com/jackc/pgx/v5/pgconn"
)

// payment_inbox statuses (system-design.txt A.6).
const (
	InboxStatusClaimed = "claimed"
	InboxStatusPosted  = "posted"
)

var (
	// ErrAlreadyPosted is returned by Claim when the receipt was already
	// posted — by this product or another — so the caller must ack and stop
	// without posting a second time.
	ErrAlreadyPosted = errors.New("payment already posted")

	// ErrWrongProduct is returned by Claim when the receipt is already
	// claimed under a DIFFERENT product than the caller's own: the webhook
	// that delivered it is misrouted, or the channel_id was reassigned
	// since the claim. Never processed locally — a real alert, not a retry.
	ErrWrongProduct = errors.New("payment receipt claimed by a different product")

	// ErrChannelUnclaimed is returned by Claim when the payment's channel has
	// no row in payment_channels (the inbox FK fails): the channel was never
	// claimed in platform. The caller still posts locally, but must alarm.
	ErrChannelUnclaimed = errors.New("payment channel has no platform claim")
)

// InboxModel is payment_inbox access: no RLS (the table has no tenant
// column — a webhook arrives with no tenant context), so every call runs
// on the Store directly, same precedent as TokenModel.
type InboxModel struct {
	Store db.Store

	// Timeout bounds each method call; zero means DefaultQueryTimeout.
	Timeout time.Duration
}

// Claim is the inbox's claim step (build-phases.txt 3g-2): claims
// mpesaReceipt for product, or tells the caller what already happened to
// it. A nil error means "claimed just now, or already claimed by THIS
// product and still 'claimed' (a retry, or a crash between claim and
// post) — proceed to post locally."
func (m InboxModel) Claim(ctx context.Context, mpesaReceipt, channelID, product string, amount moneyfmt.Money, msisdn string, raw json.RawMessage) error {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	_, err := m.Store.ClaimPaymentInbox(ctx, sqlc.ClaimPaymentInboxParams{
		MpesaReceipt: mpesaReceipt, ChannelID: channelID, Product: product, Amount: amount, Msisdn: msisdn, RawPayload: raw,
	})
	if err == nil {
		return nil // claimed just now
	}
	if !errors.Is(err, sql.ErrNoRows) {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == pgForeignKeyViolation {
			return ErrChannelUnclaimed
		}
		return err // a real database error — the caller falls back to local-only
	}

	existing, err := m.Store.GetPaymentInboxByReceipt(ctx, mpesaReceipt)
	if err != nil {
		return err
	}
	if existing.Product != product {
		return ErrWrongProduct
	}
	if existing.Status == InboxStatusPosted {
		return ErrAlreadyPosted
	}
	return nil // still 'claimed' under this product: continue
}

// MarkPosted records that product successfully posted mpesaReceipt locally.
// A no-op, not an error, if the row is already posted (a race with the
// sweeper) or — only possible if Claim's own DB call just failed, the
// platform-down fallback path — absent entirely.
func (m InboxModel) MarkPosted(ctx context.Context, mpesaReceipt string) error {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	_, err := m.Store.MarkPaymentInboxPosted(ctx, mpesaReceipt)
	return err
}

// StaleClaim is one payment_inbox row the sweeper (build-phases.txt 3g-3)
// found still 'claimed' well after it should have been posted.
type StaleClaim struct {
	MpesaReceipt string
	ChannelID    string
	RawPayload   json.RawMessage
	ClaimedAt    time.Time
}

// ListStale returns every product row still 'claimed' as of before olderThan
// (an absolute instant, not a duration — callers pass moneyfmt.Now().Add(-d)
// or equivalent). The sweeper re-drives each one through the same local
// posting path a fresh webhook delivery would have taken.
func (m InboxModel) ListStale(ctx context.Context, product string, olderThan time.Time) ([]StaleClaim, error) {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	rows, err := m.Store.ListStaleClaimedPaymentInbox(ctx, sqlc.ListStaleClaimedPaymentInboxParams{Product: product, ClaimedAt: olderThan})
	if err != nil {
		return nil, err
	}
	out := make([]StaleClaim, len(rows))
	for i, r := range rows {
		out[i] = StaleClaim{MpesaReceipt: r.MpesaReceipt, ChannelID: r.ChannelID, RawPayload: r.RawPayload, ClaimedAt: r.ClaimedAt}
	}
	return out, nil
}
