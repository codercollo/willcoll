package main

import (
	"testing"

	"github.com/codercollo/willcoll/backend/internal/shared/auth"
)

// Deny by default (docs/security/authorization.md): every registered route
// has a catalog permission or a reviewed public allowlist entry.
func TestEveryRouteIsGuardedOrPublic(t *testing.T) {
	m := newMiddlewareTestApp(t)
	var registered []string
	for _, r := range m.router.Routes() {
		registered = append(registered, auth.Key(r.Method, r.Path))
	}
	if len(registered) == 0 {
		t.Fatal("no routes registered")
	}
	for _, p := range m.app.routeGuards.Problems(registered, publicRoutes) {
		t.Error(p)
	}
}
