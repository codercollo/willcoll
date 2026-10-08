package main

import (
	"sync"
	"time"

	"github.com/google/uuid"
)

// Per-IP cap on pay-OTP requests (C3.1). A safety net only: carrier-grade NAT
// puts many tenants behind one public IP, especially on rent days, so the real
// abuse controls are the strict per-phone/per-unit caps in data.PayAccessModel
// (3 codes per 10 minutes) and the per-firm daily ceiling. Every request,
// matching phone or not, counts here so the limit reveals nothing about who
// lives where.
const (
	payOTPIPMax    = 60
	payOTPIPWindow = time.Hour
)

// firmCeilingTracker remembers which firms hit the daily OTP SMS ceiling, so
// the System page can alert. In memory like the other trackers, so a restart
// forgets it; the ceiling itself is counted from the database and survives.
type firmCeilingTracker struct {
	mu   sync.Mutex
	last map[uuid.UUID]time.Time
}

func (t *firmCeilingTracker) record(tenant uuid.UUID, at time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.last == nil {
		t.last = map[uuid.UUID]time.Time{}
	}
	t.last[tenant] = at
}

// list returns the firms that hit the ceiling in the 24 hours up to now.
func (t *firmCeilingTracker) list(now time.Time) []map[string]any {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := []map[string]any{}
	for id, at := range t.last {
		if now.Sub(at) > 24*time.Hour {
			delete(t.last, id)
			continue
		}
		out = append(out, map[string]any{"tenant_id": id, "last_hit_at": at.UTC()})
	}
	return out
}

// ipWindowLimiter is a sliding-window counter per key. In memory, so each API
// instance counts on its own (fine for one VPS; see CLAUDE.md: move to
// Postgres/Redis before running more than one instance). The zero value is
// ready to use.
type ipWindowLimiter struct {
	mu   sync.Mutex
	hits map[string][]time.Time
}

// allow records a hit for key and reports whether it is within max per window.
func (l *ipWindowLimiter) allow(key string, now time.Time, max int, window time.Duration) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.hits == nil {
		l.hits = map[string][]time.Time{}
	}
	cutoff := now.Add(-window)
	kept := l.hits[key][:0]
	for _, t := range l.hits[key] {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	if len(kept) >= max {
		l.hits[key] = kept
		return false
	}
	l.hits[key] = append(kept, now)
	// Housekeeping so idle keys do not accumulate forever.
	if len(l.hits) > 10000 {
		for k, ts := range l.hits {
			if len(ts) == 0 || !ts[len(ts)-1].After(cutoff) {
				delete(l.hits, k)
			}
		}
	}
	return true
}
