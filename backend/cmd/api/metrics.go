package main

import (
	"context"
	"expvar"
	"sync"
	"time"

	"github.com/codercollo/willcoll/backend/internal/data"
)

// reviewQueueTTL is how long a reading of the review queue is reused. The
// gauges are read on every /debug/vars scrape, and a count over payments
// should not run more often than the number can meaningfully change.
const reviewQueueTTL = 15 * time.Second

// reviewQueueGauge serves the payment review queue depth (system-design.txt
// section 5) to expvar, from a short-lived cache so scraping is cheap.
type reviewQueueGauge struct {
	read func(ctx context.Context) (data.ReviewQueueDepth, error)
	now  func() time.Time

	mu      sync.Mutex
	last    data.ReviewQueueDepth
	fetched time.Time
	failed  bool
}

func newReviewQueueGauge(read func(ctx context.Context) (data.ReviewQueueDepth, error)) *reviewQueueGauge {
	return &reviewQueueGauge{read: read, now: time.Now}
}

// depth returns the current counts. If the database cannot be read it keeps
// serving the last good value and marks it stale rather than failing the
// whole /debug/vars page.
func (g *reviewQueueGauge) depth() (d data.ReviewQueueDepth, stale bool) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if !g.fetched.IsZero() && g.now().Sub(g.fetched) < reviewQueueTTL {
		return g.last, g.failed
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	fresh, err := g.read(ctx)
	g.fetched = g.now()
	if err != nil {
		g.failed = true
		return g.last, true
	}
	g.last, g.failed = fresh, false
	return g.last, false
}

// publishBusinessMetrics adds the Willcoll-specific gauges to /debug/vars.
// payments_unmatched is the review queue depth: it should trend toward zero
// as payments move from the paybill to tenant-initiated intents.
// payments_unconfirmed counts payments the engine placed on a guess.
func (app *application) publishBusinessMetrics() {
	g := newReviewQueueGauge(app.models.Platform.ReviewQueueDepth)
	expvar.Publish("payments_unmatched", expvar.Func(func() any {
		d, _ := g.depth()
		return d.Unmatched
	}))
	expvar.Publish("payments_unconfirmed", expvar.Func(func() any {
		d, _ := g.depth()
		return d.Unconfirmed
	}))
	expvar.Publish("payments_gauges_stale", expvar.Func(func() any {
		_, stale := g.depth()
		return stale
	}))
}
