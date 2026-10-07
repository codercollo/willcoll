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

// Group tracks a set of background goroutines. The zero value is usable;
// Logger may be set to have a recovered panic logged (nil silently drops
// it, matching the case where no logger is available yet).
type Group struct {
	Logger *slog.Logger

	wg sync.WaitGroup
}

// Run runs fn in a goroutine tracked by the group, recovering any panic.
func (g *Group) Run(fn func()) {
	g.wg.Add(1)

	go func() {
		defer g.wg.Done()

		defer func() {
			if err := recover(); err != nil && g.Logger != nil {
				g.Logger.Error(fmt.Sprintf("%v", err))
			}
		}()

		fn()
	}()
}

// Wait blocks until every goroutine started by Run has returned.
func (g *Group) Wait() {
	g.wg.Wait()
}
