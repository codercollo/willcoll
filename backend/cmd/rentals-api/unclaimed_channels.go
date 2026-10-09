package main

import (
	"context"
	"sort"
	"sync"
	"time"

	platformdata "github.com/codercollo/willcoll/backend/internal/platform/data"
	"github.com/codercollo/willcoll/backend/internal/shared/payhero"
	"github.com/codercollo/willcoll/backend/pkg/moneyfmt"
)

// unclaimedChannel is one PayHero channel a collections webhook arrived for
// that has no claim in willcoll_platform.payment_channels. The payment is NOT
// posted (C3.2); the channel is a misconfiguration somebody must fix, so it is
// listed on the health page.
type unclaimedChannel struct {
	ChannelID    string    `json:"channel_id"`
	PropertyID   string    `json:"property_id,omitempty"`
	PropertyName string    `json:"property_name,omitempty"`
	Count        int       `json:"count"`
	LastSeenAt   time.Time `json:"last_seen_at"`
}

// unclaimedChannelTracker remembers, for the trailing 24h, every distinct
// channel that hit the unclaimed-channel path (in memory, like the other
// health counters: a restart clears it, the next webhook re-raises it).
type unclaimedChannelTracker struct {
	mu      sync.Mutex
	entries map[string]*unclaimedChannel
}

func (t *unclaimedChannelTracker) record(at time.Time, channelID, propertyID, propertyName string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.entries == nil {
		t.entries = map[string]*unclaimedChannel{}
	}
	t.prune(at)
	e, ok := t.entries[channelID]
	if !ok {
		e = &unclaimedChannel{ChannelID: channelID}
		t.entries[channelID] = e
	}
	e.Count++
	e.LastSeenAt = at
	if propertyID != "" {
		e.PropertyID, e.PropertyName = propertyID, propertyName
	}
}

// list returns the channels seen in the 24 hours up to now, most recent first.
// Never nil, so it encodes as [] rather than null.
func (t *unclaimedChannelTracker) list(now time.Time) []unclaimedChannel {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.prune(now)
	out := make([]unclaimedChannel, 0, len(t.entries))
	for _, e := range t.entries {
		out = append(out, *e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LastSeenAt.After(out[j].LastSeenAt) })
	return out
}

// prune drops entries last seen more than 24h ago. Caller holds the lock.
func (t *unclaimedChannelTracker) prune(at time.Time) {
	cutoff := at.Add(-24 * time.Hour)
	for k, e := range t.entries {
		if e.LastSeenAt.Before(cutoff) {
			delete(t.entries, k)
		}
	}
}

// recordUnclaimedReceipt writes the payment to willcoll_platform's
// unclaimed_receipts so received money is never only a log line. Idempotent
// per receipt (PayHero redelivers). A write that keeps failing is logged with
// the full payload and counted on the health page's inbox fallbacks' sibling,
// so it cannot pass unnoticed.
func (app *application) recordUnclaimedReceipt(payload *payhero.CollectionsWebhookPayload, amount moneyfmt.Money) {
	err := retryWebhook(func(ctx context.Context) error {
		_, err := app.platform.Unclaimed.Record(ctx, payload.MpesaReceipt, payload.ChannelID, platformdata.ProductRentals, amount, payload.Msisdn, payload.Raw)
		return err
	})
	if err != nil {
		app.logger.Error("payment_inbox: ALERT could not record an unclaimed payment; payload follows for manual recovery",
			"receipt", payload.MpesaReceipt, "channel_id", payload.ChannelID, "amount", payload.Amount,
			"msisdn", payload.Msisdn, "payload", string(payload.Raw), "error", err)
		app.unclaimedRecordFailures.record(time.Now())
	}
}

// alarmUnclaimedChannel logs at ERROR, with the channel id, the property (or
// properties) using it and the full payload (the recovery trail: nothing is
// posted for an unclaimed channel), and records the channel for the health
// page.
func (app *application) alarmUnclaimedChannel(payload *payhero.CollectionsWebhookPayload) {
	now := moneyfmt.Now()
	ctx, cancel := context.WithTimeout(context.Background(), webhookProcessTimeout)
	props, err := app.models.Resolver.PropertiesForChannel(ctx, payload.ChannelID)
	cancel()
	if err != nil {
		app.logger.Warn("unclaimed channel: could not look up its property", "channel_id", payload.ChannelID, "error", err)
	}

	if len(props) == 0 {
		app.logger.Error("payment_inbox: ALERT collections payment on a channel with no platform claim; NOT posted",
			"channel_id", payload.ChannelID, "receipt", payload.MpesaReceipt, "amount", payload.Amount,
			"msisdn", payload.Msisdn, "property_id", "", "payload", string(payload.Raw))
		app.unclaimedChannels.record(now, payload.ChannelID, "", "")
		return
	}
	for _, p := range props {
		app.logger.Error("payment_inbox: ALERT collections payment on a channel with no platform claim; NOT posted",
			"channel_id", payload.ChannelID, "receipt", payload.MpesaReceipt, "amount", payload.Amount,
			"msisdn", payload.Msisdn, "property_id", p.ID, "property", p.Name, "manager_id", p.TenantID,
			"payload", string(payload.Raw))
		app.unclaimedChannels.record(now, payload.ChannelID, p.ID.String(), p.Name)
	}
}
