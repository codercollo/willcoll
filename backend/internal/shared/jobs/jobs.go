// Package jobs holds the product-agnostic background-goroutine pattern:
// run a function in a tracked goroutine, recovering any panic so a failure
// in a background job (email, SMS, PDF render) never crashes the process,
// and let a graceful shutdown wait for every in-flight one to finish.
package jobs

import (
	"fmt"
	"log/slog"
	"sync"
)

// Group tracks a set of background goroutines. The zero value is usable and
// Run is safe to call from many goroutines at once (a webhook handler does).
type Group struct {
	wg sync.WaitGroup
}

// Run runs fn in a goroutine tracked by the group, recovering any panic. A
// recovered panic is logged to logger; nil silently drops it. The logger is
// an argument, not a field, so concurrent callers never write shared state
// that the goroutines they started are reading.
func (g *Group) Run(logger *slog.Logger, fn func()) {
	g.wg.Add(1)

	go func() {
		defer g.wg.Done()

		defer func() {
			if err := recover(); err != nil && logger != nil {
				logger.Error(fmt.Sprintf("%v", err))
			}
		}()

		fn()
	}()
}

// Wait blocks until every goroutine started by Run has returned.
func (g *Group) Wait() {
	g.wg.Wait()
}
