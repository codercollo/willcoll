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
// allowlist, the step-up routes are tied to auth.StepUpPermissions, and every
// route with a field-level rule has a variant row for each protected field.

func TestRouteMatrixGoldenCoversExactlyTheRegisteredRoutes(t *testing.T) {
	m := newMiddlewareTestApp(t)
	all, err := routematrix.Load()
	if err != nil {
		t.Fatal(err)
	}
	golden, ok := all[routematrix.APIRentals]
	if !ok {
		t.Fatalf("no %s section in the golden file", routematrix.APIRentals)
	}
	for key, g := range m.app.routeGuards.Entries() {
		if g.Permission == "" && g.PublicReason == "" {
			t.Errorf("%s has neither a permission nor a public-allowlist reason", key)
		}
		if _, ok := golden[key]; !ok {
			t.Errorf("%s is registered but not in the golden matrix", key)
		}
	}
	for key := range golden {
		if _, ok := m.app.routeGuards.Lookup(routematrix.BaseKey(key)); !ok {
			t.Errorf("%s is in the golden matrix but is not a registered route", key)
		}
	}
}

// The in-handler step-up routes are registered routes guarded by a step-up
// permission or by the permission that decides the protected field.
func TestRentalsInHandlerStepUpRoutesAreRegistered(t *testing.T) {
	m := newMiddlewareTestApp(t)
	for key := range stepUpInHandlerRoutes {
		if _, ok := m.app.routeGuards.Lookup(key); !ok {
			t.Errorf("stepUpInHandlerRoutes lists %s, which is not a registered route", key)
		}
		if stepUpGatedRoutes[key] {
			t.Errorf("%s is listed as both gated and in-handler", key)
		}
	}
	for key, g := range m.app.routeGuards.Entries() {
		if slices.Contains(auth.StepUpPermissions, g.Permission) && stepUpInHandlerRoutes[key] {
			t.Errorf("%s is guarded by a step-up permission, so it must be gated by middleware", key)
		}
	}
}

// Every route with a field-level rule has a golden variant row for each protected
// field it lists, and the matrix actually runs each.
func TestEveryFieldCheckedRouteHasItsVariantRows(t *testing.T) {
	m := newMiddlewareTestApp(t)
	all, err := routematrix.Load()
	if err != nil {
		t.Fatal(err)
	}
	golden := all[routematrix.APIRentals]
	for route, variants := range routesWithFieldChecks {
		if _, ok := m.app.routeGuards.Lookup(route); !ok {
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
		if _, ok := m.app.routeGuards.Lookup(v.route); !ok {
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
	for key := range variantsOnPendingPayment {
		if !defined[key] {
			t.Errorf("variantsOnPendingPayment names %q, which matrixVariants() does not define", key)
		}
	}
}

// fieldCheckMarkers are the calls that make a handler's answer depend on what the
// request changes: the channel rule, the shared money-field guard, and the rule
// that a recorder who cannot confirm leaves a payment pending.
var fieldCheckMarkers = []string{
	"app.requireOwnerForChannelChange(", "app.requireOwnerForPayoutChange(", "app.guardUnitMoneyFields(", "app.refuseMoneyChange(",
	"pending := !app.permitted(", "data.ErrSelfConfirm",
}

// A handler that applies a value-dependent check is on the reviewed list. This
// reads the handlers' source, so a new field check cannot slip in unlisted.
func TestEveryFieldCheckInTheCodeIsListed(t *testing.T) {
	m := newMiddlewareTestApp(t)
	for _, r := range m.router.Routes() {
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
	m := newMiddlewareTestApp(t)
	if m.app.markHandlers {
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
