package routematrix

import (
	"net/http"
	"strings"
	"testing"

	"github.com/codercollo/willcoll/backend/internal/shared/auth"
)

// Rules tell the invariant checks which routes are which, for one API. Route
// maps are keyed by the registered route ("METHOD /path"), never by a variant row.
type Rules struct {
	// StepUp is the set of routes that demand a recent password re-entry before
	// the handler runs. The route's own row must show a step-up cell.
	StepUp map[string]bool
	// StepUpInHandler is the set of routes that demand step-up only for some
	// requests, decided inside the handler (a role change, a channel change). The
	// route's plain row never meets it; at least one of its variant rows must.
	StepUpInHandler map[string]bool
	// ScopedResource reports whether a route names a property-scoped resource
	// (a path id of a property, unit, lease, ...), by its full path.
	ScopedResource func(path string) bool
	// FirmRoute reports whether a route is a firm's own data, which the Super
	// Admin must never reach.
	FirmRoute func(path string) bool
	// Internal reports whether a route is service-to-service (/internal/v1/*).
	Internal func(path string) bool
}

func (r Rules) scoped(p string) bool   { return r.ScopedResource != nil && r.ScopedResource(p) }
func (r Rules) firm(p string) bool     { return r.FirmRoute != nil && r.FirmRoute(p) }
func (r Rules) internal(p string) bool { return r.Internal != nil && r.Internal(p) }

var staffRoles = map[string]string{
	"viewer": "viewer", "officer": "officer", "officer_scoped": "officer", "accountant": "accountant", "staff_owner": "owner",
}

// CheckInvariants asserts, in code and not only in the golden file, the rules
// docs/security/authorization.md promises, and that the observed answers agree
// with the permission catalog. Every disagreement is reported as an error.
//
// A row is either a route's plain row or a named variant of it (a realistic
// body, see VariantKey). Both are held to the invariants that do not depend on
// what the request changes; only the plain row is held to "whoever holds the
// permission got past the permission check", because a variant exists precisely
// to show the field-level rule that refuses a permitted caller.
func CheckInvariants(t testing.TB, guards *auth.RouteTable, got Matrix, r Rules) {
	t.Helper()
	entries := guards.Entries()
	for key := range entries {
		if _, ok := got[key]; !ok {
			t.Errorf("%s is a registered route but is missing from the matrix", key)
		}
	}
	asked := map[string]bool{}        // base route -> some row asked for step-up
	askedOnPlain := map[string]bool{} // base route -> the plain row asked for step-up
	for key, row := range got {
		base := BaseKey(key)
		g, ok := entries[base]
		if !ok {
			t.Errorf("%s is in the matrix but is not a registered route", key)
			continue
		}
		method, path, _ := strings.Cut(base, " ")
		public := g.PublicReason != ""
		internal := r.internal(path)
		variant := IsVariant(key)

		// Nobody without a session gets in to a guarded route.
		if !public {
			for _, p := range []string{"anonymous", "pay_token", "service"} {
				if IsSuccess(row[p]) {
					t.Errorf("%s: %s got %s on a guarded route", key, p, row[p])
				}
			}
			if row["anonymous"] != "401" {
				t.Errorf("%s: anonymous got %s, want 401", key, row["anonymous"])
			}
		}

		// Service credential: 2xx only on /internal/v1/*. User sessions: never there.
		if internal {
			// The credential is let in (the handler may still reject the empty
			// body); everyone else is turned away at the door.
			if c := row["service"]; c == "401" || c == "403" {
				t.Errorf("%s: the service credential was refused (%s) on an internal route", key, c)
			}
			for _, p := range Principals {
				if c := row[p]; p != "service" && c != "401" && c != "403" {
					t.Errorf("%s: %s got %s on an internal route; only the service credential may enter", key, p, c)
				}
			}
		} else if IsSuccess(row["service"]) && (!public || !IsSuccess(row["anonymous"])) {
			t.Errorf("%s: the service credential got %s outside /internal/v1/*", key, row["service"])
		}

		// Tenant pay token: 2xx only where anyone is let in (public), and on the
		// pay surface only for its own unit (the caller asks for unit A1).
		if IsSuccess(row["pay_token"]) && !public {
			t.Errorf("%s: the pay token got %s on a guarded route", key, row["pay_token"])
		}

		// Viewer: read-only, and never an export.
		if !public && method != http.MethodGet && IsSuccess(row["viewer"]) {
			t.Errorf("%s: viewer got %s on a write", key, row["viewer"])
		}
		if g.Permission == auth.ExportsCreate && IsSuccess(row["viewer"]) {
			t.Errorf("%s: viewer got %s on an export", key, row["viewer"])
		}

		// Super Admin: never a firm's ledger or data.
		if !public && r.firm(path) && IsSuccess(row["super_admin"]) {
			t.Errorf("%s: the Super Admin got %s on a firm route", key, row["super_admin"])
		}

		// A scoped officer asking for property B's resource: always 404, refused by
		// scope before any handler runs (so never "404h").
		if !public && r.scoped(path) && row["officer_scoped"] != "404" {
			t.Errorf("%s: the scoped officer got %s for property B's resource, want 404 (refused by scope, handler not reached)", key, row["officer_scoped"])
		}

		// A step-up cell is always followed through: the same request, repeated
		// with a fresh window, must have been run, and the window must have opened
		// the gate (a refusal that survives step-up is a different rule).
		for _, p := range Principals {
			cell := row[p]
			if !AskedForStepUp(cell) {
				continue
			}
			asked[base] = true
			if !variant {
				askedOnPlain[base] = true
			}
			if !strings.Contains(cell, afterStepUp) {
				t.Errorf("%s: %s was asked for step-up but the request was never repeated after it", key, p)
			}
			if after := Refusal(cell); after == "401" || after == "403" {
				t.Errorf("%s: %s is still refused (%s) after step-up", key, p, after)
			}
		}

		// The answers agree with the permission catalog.
		if public || internal || g.Permission == "" {
			continue
		}
		expect := func(p string, permitted bool) {
			if p == "officer_scoped" && r.scoped(path) {
				return // 404 first, by design
			}
			cell := row[p]
			if permitted && !variant && strings.HasPrefix(cell, "403") {
				t.Errorf("%s: %s holds %s but was refused (%s)", key, p, g.Permission, cell)
			}
			if !permitted && cell != "403" && cell != "401" {
				t.Errorf("%s: %s does not hold %s but got %s, want 403", key, p, g.Permission, cell)
			}
		}
		for p, role := range staffRoles {
			expect(p, auth.StaffPermits(role, g.Permission))
		}
		expect("firm_login", auth.FirmLoginPermits(g.Permission))
		expect("super_admin", auth.AdminPermits(g.Permission))
	}

	// Step-up appears on exactly the declared routes.
	for base := range asked {
		if !r.StepUp[base] && !r.StepUpInHandler[base] {
			t.Errorf("%s: someone was asked for step-up, but the route is not a declared step-up route", base)
		}
	}
	for base := range r.StepUp {
		if !askedOnPlain[base] {
			t.Errorf("%s is a declared step-up route but its own row shows nobody asked for step-up", base)
		}
	}
	for base := range r.StepUpInHandler {
		if !asked[base] {
			t.Errorf("%s is a declared in-handler step-up route but no variant row shows anyone asked for step-up", base)
		}
	}
}
