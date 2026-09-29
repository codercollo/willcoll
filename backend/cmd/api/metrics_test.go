package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/codercollo/willcoll/backend/internal/data"
)

func TestReviewQueueGauge(t *testing.T) {
	now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	calls := 0
	depth := data.ReviewQueueDepth{Unmatched: 7, Unconfirmed: 2}
	var readErr error

	g := newReviewQueueGauge(func(context.Context) (data.ReviewQueueDepth, error) {
		calls++
		return depth, readErr
	})
	g.now = func() time.Time { return now }

	if d, stale := g.depth(); d.Unmatched != 7 || d.Unconfirmed != 2 || stale || calls != 1 {
		t.Fatalf("first read = %+v stale=%v calls=%d", d, stale, calls)
	}

	// Scrapes inside the TTL reuse the reading instead of hitting the database.
	now = now.Add(reviewQueueTTL - time.Second)
	depth.Unmatched = 99
	if d, _ := g.depth(); d.Unmatched != 7 || calls != 1 {
		t.Errorf("within the TTL: %+v, calls %d; want the cached reading", d, calls)
	}

	// After the TTL it refreshes.
	now = now.Add(2 * time.Second)
	if d, _ := g.depth(); d.Unmatched != 99 || calls != 2 {
		t.Errorf("after the TTL: %+v, calls %d; want a fresh reading", d, calls)
	}

	// A database failure serves the last good value, flagged stale, and does
	// not take /debug/vars down.
	now = now.Add(reviewQueueTTL + time.Second)
	readErr = errors.New("database is down")
	d, stale := g.depth()
	if d.Unmatched != 99 || !stale {
		t.Errorf("on failure: %+v stale=%v, want the last good value marked stale", d, stale)
	}

	// And it recovers.
	now = now.Add(reviewQueueTTL + time.Second)
	readErr, depth.Unmatched = nil, 3
	if d, stale := g.depth(); d.Unmatched != 3 || stale {
		t.Errorf("after recovery: %+v stale=%v", d, stale)
	}
}
