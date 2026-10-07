package main

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/codercollo/willcoll/backend/internal/shared/payhero"
	"github.com/codercollo/willcoll/backend/pkg/moneyfmt"
)

// unclaimedChannel is one PayHero channel a collections webhook arrived for
// that has no claim in willcoll_platform.payment_channels (the payment_inbox
// FK failed). The payment is still posted locally, but the channel is a
// misconfiguration somebody must fix, so it is listed on the health page.
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

// alarmUnclaimedChannel logs at ERROR, with the channel id and the property
// (or properties) using it, and records the channel for the health page. The
// caller still posts the payment locally: money is never dropped for this.
func (app *application) alarmUnclaimedChannel(payload *payhero.CollectionsWebhookPayload) {
	now := moneyfmt.Now()
	ctx, cancel := context.WithTimeout(context.Background(), webhookProcessTimeout)
	props, err := app.models.Resolver.PropertiesForChannel(ctx, payload.ChannelID)
	cancel()
	if err != nil {
		app.logger.Warn("unclaimed channel: could not look up its property", "channel_id", payload.ChannelID, "error", err)
	}

	if len(props) == 0 {
		app.logger.Error("payment_inbox: ALERT collections payment on a channel with no platform claim; posting locally",
			"channel_id", payload.ChannelID, "receipt", payload.MpesaReceipt, "property_id", "")
		app.unclaimedChannels.record(now, payload.ChannelID, "", "")
		return
	}
	for _, p := range props {
		app.logger.Error("payment_inbox: ALERT collections payment on a channel with no platform claim; posting locally",
			"channel_id", payload.ChannelID, "receipt", payload.MpesaReceipt,
			"property_id", p.ID, "property", p.Name, "manager_id", p.TenantID)
		app.unclaimedChannels.record(now, payload.ChannelID, p.ID.String(), p.Name)
	}
}
