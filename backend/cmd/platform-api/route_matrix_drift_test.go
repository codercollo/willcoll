package main

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/codercollo/willcoll/backend/internal/shared/auth"
	"github.com/codercollo/willcoll/backend/internal/shared/auth/routematrix"
	"github.com/gin-gonic/gin"
)

// Drift checks that need no database (the matrix itself is
// route_matrix_integration_test.go): the golden file lists exactly the routes
// this API registers, every one of them guarded or on the reviewed public
// allowlist, the step-up routes agree with auth.StepUpPermissions, and every
// route with a field-level rule has a variant row for each protected field.

func newRoutedApp(t *testing.T) *application {
	t.Helper()
	app := &application{}
	app.config.cors.trustedOrigins = []string{"http://localhost:3000"}
	if _, err := app.routes(); err != nil {
		t.Fatal(err)
	}
	return app
}

func TestRouteMatrixGoldenCoversExactlyTheRegisteredRoutes(t *testing.T) {
	app := newRoutedApp(t)
	all, err := routematrix.Load()
	if err != nil {
		t.Fatal(err)
	}
	golden, ok := all[routematrix.APIPlatform]
	if !ok {
		t.Fatalf("no %s section in the golden file", routematrix.APIPlatform)
	}
	for key, g := range app.routeGuards.Entries() {
		if g.Permission == "" && g.PublicReason == "" {
			t.Errorf("%s has neither a permission nor a public-allowlist reason", key)
		}
		if _, ok := golden[key]; !ok {
			t.Errorf("%s is registered but not in the golden matrix", key)
		}
	}
	for key := range golden {
		if _, ok := app.routeGuards.Lookup(routematrix.BaseKey(key)); !ok {
			t.Errorf("%s is in the golden matrix but is not a registered route", key)
		}
	}
}

// The routes that ask for step-up are the routes guarded by a step-up
// permission: gated by middleware (stepUpGatedRoutes) or inside the handler
// (stepUpInHandlerRoutes).
func TestPlatformStepUpRoutesMatchTheStepUpPermissions(t *testing.T) {
	app := newRoutedApp(t)
	for key, g := range app.routeGuards.Entries() {
		if slices.Contains(auth.StepUpPermissions, g.Permission) && !stepUpGatedRoutes[key] && !stepUpInHandlerRoutes[key] {
			t.Errorf("%s is guarded by %s (step-up) but is neither gated nor an in-handler step-up route", key, g.Permission)
		}
	}
	for _, set := range []map[string]bool{stepUpGatedRoutes, stepUpInHandlerRoutes} {
		for key := range set {
			g, ok := app.routeGuards.Lookup(key)
			if !ok {
				t.Errorf("a step-up table lists %s, which is not a registered route", key)
				continue
			}
			if !slices.Contains(auth.StepUpPermissions, g.Permission) {
				t.Errorf("%s asks for step-up but %s is not in auth.StepUpPermissions", key, g.Permission)
			}
		}
	}
	for key := range stepUpGatedRoutes {
		if stepUpInHandlerRoutes[key] {
			t.Errorf("%s is listed as both gated and in-handler", key)
		}
	}
}

// Every route with a field-level rule has a golden variant row for each
// protected field it lists, and every variant the matrix runs belongs to a
// listed field check or is a plain realistic body of a gated route.
func TestEveryFieldCheckedRouteHasItsVariantRows(t *testing.T) {
	app := newRoutedApp(t)
	all, err := routematrix.Load()
	if err != nil {
		t.Fatal(err)
	}
	golden := all[routematrix.APIPlatform]
	for route, variants := range routesWithFieldChecks {
		if _, ok := app.routeGuards.Lookup(route); !ok {
			t.Errorf("routesWithFieldChecks lists %s, which is not a registered route", route)
			continue
		}
		if len(variants) == 0 {
			t.Errorf("%s is listed with no variant rows", route)
		}
		for _, name := range variants {
			if _, ok := golden[routematrix.VariantKey(route, name)]; !ok {
				t.Errorf("%s has a field-level rule but the golden matrix has no row %q", route, routematrix.VariantKey(route, name))
			}
		}
	}
	defined := map[string]bool{}
	for _, v := range matrixVariants() {
		defined[v.key()] = true
		if _, ok := app.routeGuards.Lookup(v.route); !ok {
			t.Errorf("variant %s is for a route that is not registered", v.key())
		}
	}
	for route, variants := range routesWithFieldChecks {
		for _, name := range variants {
			if !defined[routematrix.VariantKey(route, name)] {
				t.Errorf("routesWithFieldChecks lists %q but matrixVariants() does not run it", routematrix.VariantKey(route, name))
			}
		}
	}
}

// fieldCheckMarkers are the calls that make a handler's answer depend on what
// the request changes: an in-handler step-up demand, or the owner-role rule.
var fieldCheckMarkers = []string{"app.stepUpSatisfied(", "app.staffActorMayNotChangeOwner("}

// A handler that applies a value-dependent check is on the reviewed list. This
// reads the handlers' source, so a new field check cannot slip in unlisted.
func TestEveryFieldCheckInTheCodeIsListed(t *testing.T) {
	app := &application{}
	app.config.cors.trustedOrigins = []string{"http://localhost:3000"}
	router, err := app.routes()
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range router.Routes() {
		src := handlerSource(t, r.HandlerFunc)
		for _, marker := range fieldCheckMarkers {
			if strings.Contains(src, marker) {
				if _, listed := routesWithFieldChecks[auth.Key(r.Method, r.Path)]; !listed {
					t.Errorf("%s %s calls %s but is not in routesWithFieldChecks: give it a variant row for the protected field", r.Method, r.Path, marker)
				}
			}
		}
	}
}

// handlerSource returns the source of a handler method of *application, with
// comment lines dropped.
func handlerSource(t *testing.T, handler gin.HandlerFunc) string {
	t.Helper()
	wrapperName := runtime.FuncForPC(reflect.ValueOf(handler).Pointer()).Name()
	methodName := strings.TrimSuffix(wrapperName[strings.LastIndex(wrapperName, ".")+1:], "-fm")
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	decl := "func (app *application) " + methodName + "("
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		b, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		idx := strings.Index(string(b), decl)
		if idx < 0 {
			continue
		}
		var out []string
		for i, line := range strings.Split(string(b[idx:]), "\n") {
			if i > 0 && strings.HasPrefix(line, "func ") {
				break
			}
			if !strings.HasPrefix(strings.TrimSpace(line), "//") {
				out = append(out, line)
			}
		}
		return strings.Join(out, "\n")
	}
	return "" // a closure or a method of another type: nothing to scan
}

// The handler-reached marker is for tests only: off in a plain application, and
// never switched on by non-test code.
func TestHandlerMarkerIsOffByDefault(t *testing.T) {
	app := newRoutedApp(t)
	if app.markHandlers {
		t.Fatal("markHandlers is on in an application nobody configured")
	}
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		b, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for _, bad := range []string{"markHandlers = true", "markHandlers: true", "markHandlers:true"} {
			if strings.Contains(string(b), bad) {
				t.Errorf("%s switches the test-only handler marker on (%q)", file, bad)
			}
		}
	}
}
