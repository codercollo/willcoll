package main

import (
	"strings"
	"testing"

	"github.com/codercollo/willcoll/backend/internal/shared/auth"
)

// Deny by default (docs/security/authorization.md): every registered route
// has a catalog permission or a reviewed public allowlist entry.
func TestEveryRouteIsGuardedOrPublic(t *testing.T) {
	app := &application{}
	app.config.cors.trustedOrigins = []string{"http://localhost:3000"}
	router, err := app.routes()
	if err != nil {
		t.Fatal(err)
	}
	var registered []string
	for _, r := range router.Routes() {
		registered = append(registered, auth.Key(r.Method, r.Path))
	}
	if len(registered) == 0 {
		t.Fatal("no routes registered")
	}
	for _, p := range app.routeGuards.Problems(registered, publicRoutes) {
		t.Error(p)
	}
}

// /debug/vars lives only on the internal metrics listener, never on the
// public router (authorization.md M13).
func TestDebugVarsNotOnPublicRouter(t *testing.T) {
	app := &application{}
	app.config.cors.trustedOrigins = []string{"http://localhost:3000"}
	router, err := app.routes()
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range router.Routes() {
		if strings.HasPrefix(r.Path, "/debug") {
			t.Errorf("public router registers %s %s; /debug/* belongs on the internal metrics listener only", r.Method, r.Path)
		}
	}
}
