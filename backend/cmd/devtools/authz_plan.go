package main

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	platformdata "github.com/codercollo/willcoll/backend/internal/platform/data"
	"github.com/codercollo/willcoll/backend/internal/shared/auth"
	"github.com/google/uuid"
)

// The authorization backfill's planner (docs/security/c4-spec.md C4.1). Pure
// functions only: no database, no clock. The same plan feeds the dry run, the
// apply and the tests, so what the dry run reports is exactly what apply does.
//
// THE RULE: a backfill may only narrow or keep what a principal can do, never
// widen it. "What a principal can do" is defined once, in effectiveOf, from the
// runtime's own rules (exact-string module_access, fail-closed property scope,
// unknown role = no permissions, non-active staff = nothing). A change is only
// ever made if effectiveOf(after) is a subset of effectiveOf(before); the unit
// tests enforce it across random data and the integration test across a real
// database.

// Change categories. The first group are state changes (each writes one audit
// row); the second group is reported and never changed.
const (
	catRole             = "role_invalid"
	catModuleAccess     = "module_access_normalized"
	catPropertyAccess   = "property_access_pruned"
	catSessionsInactive = "sessions_of_inactive_staff"
	catSessionsRemoved  = "sessions_of_removed_staff"
	catOrphanStaff      = "orphan_staff_deactivated"

	catModuleLapsed = "module_access_lapsed"
	catFirmNoOwner  = "firm_login_without_owner"
)

// changeCategories is the order the report prints state changes in.
var changeCategories = []string{catRole, catModuleAccess, catPropertyAccess, catSessionsInactive, catSessionsRemoved, catOrphanStaff}

// reportOnlyCategories are findings no apply ever changes.
var reportOnlyCategories = []string{catModuleLapsed, catFirmNoOwner}

// staffState is the part of a staff row the backfill may change.
type staffState struct {
	Role           *string // nil = NULL
	ModuleAccess   []string
	PropertyAccess json.RawMessage // the jsonb value as stored
	Status         string
}

// staffRow is a staff row as the backfill reads it.
type staffRow struct {
	ID        uuid.UUID
	ManagerID uuid.UUID
	Version   int32
	staffState
}

// authzWorld is everything the plan needs to know besides the staff row.
type authzWorld struct {
	// Managers is the set of firms that exist.
	Managers map[uuid.UUID]bool
	// HeldModules is firm -> products it holds (manager_modules trialing or
	// active), the same set M8 validates module_access writes against.
	HeldModules map[uuid.UUID]map[string]bool
	// Properties is every live (not archived) property id -> its firm.
	Properties map[uuid.UUID]uuid.UUID
	// LiveSessions is staff id -> live session count.
	LiveSessions map[uuid.UUID]int
}

// authzChange is one thing the backfill changes or reports, for one staff row.
type authzChange struct {
	Category  string
	StaffID   uuid.UUID
	ManagerID uuid.UUID
	Reason    string // no names, phones or raw values: counts and rule names only
}

// staffPlan is the outcome of planning one staff row.
type staffPlan struct {
	Row      staffRow
	After    staffState
	Changes  []authzChange // state changes
	Findings []authzChange // report only
	// RevokeSessions says the member's live sessions are to be destroyed.
	RevokeSessions bool
}

// stateChanged reports whether the row itself must be written.
func (p staffPlan) stateChanged() bool {
	b, a := p.Row.staffState, p.After
	if (b.Role == nil) != (a.Role == nil) || (b.Role != nil && *b.Role != *a.Role) {
		return true
	}
	return b.Status != a.Status || !slices.Equal(b.ModuleAccess, a.ModuleAccess) || !jsonEqual(b.PropertyAccess, a.PropertyAccess)
}

func (p *staffPlan) change(cat, reason string) {
	p.Changes = append(p.Changes, authzChange{Category: cat, StaffID: p.Row.ID, ManagerID: p.Row.ManagerID, Reason: reason})
}

func (p *staffPlan) finding(cat, reason string) {
	p.Findings = append(p.Findings, authzChange{Category: cat, StaffID: p.Row.ID, ManagerID: p.Row.ManagerID, Reason: reason})
}

var knownProducts = []string{platformdata.ProductRentals, platformdata.ProductEstates, platformdata.ProductWaste}

func isProduct(s string) bool { return slices.Contains(knownProducts, s) }

var validRoles = []string{platformdata.RoleOwner, platformdata.RoleAccountant, platformdata.RoleOfficer, platformdata.RoleViewer}

func validRole(r *string) bool { return r != nil && slices.Contains(validRoles, *r) }

// planStaff decides what the backfill does to one staff row.
func planStaff(r staffRow, w authzWorld) staffPlan {
	p := staffPlan{Row: r}
	p.After = staffState{
		Role: r.Role, Status: r.Status,
		ModuleAccess:   slices.Clone(r.ModuleAccess),
		PropertyAccess: slices.Clone(r.PropertyAccess),
	}
	if p.After.ModuleAccess == nil {
		p.After.ModuleAccess = []string{}
	}

	// 1. Role. A NULL or unknown role grants NOTHING today (RoleAtLeast and
	// StaffPermits fail closed), and viewer grants something, so moving it to
	// viewer alone would widen. It is made viewer (a valid, least-privilege role)
	// AND kept inactive: the account stays exactly as unusable as it is now until
	// an owner re-enables it on purpose.
	if !validRole(r.Role) {
		viewer := platformdata.RoleViewer
		p.After.Role = &viewer
		kind := "invalid"
		if r.Role == nil {
			kind = "NULL"
		}
		reason := "role is " + kind + " (grants nothing today); set to viewer"
		if p.After.Status == platformdata.StaffActive || p.After.Status == platformdata.StaffInvited {
			p.After.Status = platformdata.StaffDisabled
			reason += " and the account disabled, so it gains nothing until an owner re-enables it"
		}
		p.change(catRole, reason)
	}

	// 2. Orphans: a staff row whose firm does not exist.
	orphan := !w.Managers[r.ManagerID]
	if orphan && p.After.Status != platformdata.StaffDisabled {
		p.After.Status = platformdata.StaffDisabled
		p.change(catOrphanStaff, "the firm does not exist; account disabled")
	}

	// 3. module_access.
	mods, dropped := normalizeModules(r.ModuleAccess)
	p.After.ModuleAccess = mods
	if dropped.total() > 0 {
		reason := fmt.Sprintf("dropped %d entries from module_access (%s)", dropped.total(), dropped.describe())
		p.change(catModuleAccess, reason)
	}
	if !orphan {
		for _, m := range mods {
			if !w.HeldModules[r.ManagerID][m] {
				p.finding(catModuleLapsed, "module_access names a product the firm no longer holds; reported only (M8: existing users are not broken)")
				break
			}
		}
	}

	// 4. property_access.
	if raw, why, changed := prunePropertyAccess(r.PropertyAccess, p.After, r.ManagerID, w); changed {
		p.After.PropertyAccess = raw
		p.change(catPropertyAccess, why)
	}

	// 5. Sessions of staff that are no longer active.
	if p.After.Status != platformdata.StaffActive && w.LiveSessions[r.ID] > 0 {
		p.RevokeSessions = true
		cat := catSessionsInactive
		why := fmt.Sprintf("revoked %d live session(s) of a staff member who is %s", w.LiveSessions[r.ID], p.After.Status)
		if orphan {
			cat, why = catOrphanStaff, fmt.Sprintf("revoked %d live session(s) of an orphaned staff member", w.LiveSessions[r.ID])
		}
		p.change(cat, why)
	}
	return p
}

// droppedModules counts what normalizeModules removed, by reason.
type droppedModules struct{ unknown, caseVariant, duplicate int }

func (d droppedModules) total() int { return d.unknown + d.caseVariant + d.duplicate }

func (d droppedModules) describe() string {
	var parts []string
	if d.caseVariant > 0 {
		parts = append(parts, fmt.Sprintf("%d with the wrong case or spacing, not lowercased because that would grant access the member does not have today", d.caseVariant))
	}
	if d.unknown > 0 {
		parts = append(parts, fmt.Sprintf("%d unknown", d.unknown))
	}
	if d.duplicate > 0 {
		parts = append(parts, fmt.Sprintf("%d duplicate", d.duplicate))
	}
	return strings.Join(parts, ", ")
}

// normalizeModules keeps each known product once, in its original order.
//
// The spec says "normalize to lowercase", but the runtime compares module_access
// entries as exact strings, so "Rentals" opens nothing today and lowercasing it
// would GRANT rentals. That breaks the narrow-only rule, so a wrong-case entry is
// dropped (access is unchanged) and reported for the owner to re-grant on
// purpose.
func normalizeModules(in []string) (out []string, d droppedModules) {
	out = []string{}
	for _, e := range in {
		switch {
		case isProduct(e):
			if slices.Contains(out, e) {
				d.duplicate++
			} else {
				out = append(out, e)
			}
		case isProduct(strings.ToLower(strings.TrimSpace(e))):
			d.caseVariant++
		default:
			d.unknown++
		}
	}
	return out, d
}

// prunePropertyAccess repairs a property_access value without widening it.
//
// At runtime a value that does not decode into {product: [uuid]} fails closed
// (the member can reach no property), and an empty or missing list means ALL
// properties. So dropping every id of a list would silently turn "nothing"
// into "everything": a list left with no live property is stored as the nil
// uuid, which no property has, keeping it restricted to nothing. Owners are
// never restricted whatever the value says, so theirs is left alone.
func prunePropertyAccess(raw json.RawMessage, after staffState, tenant uuid.UUID, w authzWorld) (out json.RawMessage, why string, changed bool) {
	if after.Role != nil && *after.Role == platformdata.RoleOwner || len(raw) == 0 {
		return raw, "", false
	}
	var typed map[string][]uuid.UUID
	if err := json.Unmarshal(raw, &typed); err != nil {
		// Malformed: every product is closed today. Keep it closed for the
		// products the value names and the member's own, drop the junk.
		closed := map[string][]uuid.UUID{}
		var keys map[string]json.RawMessage
		_ = json.Unmarshal(raw, &keys)
		for k := range keys {
			if isProduct(k) {
				closed[k] = []uuid.UUID{uuid.Nil}
			}
		}
		for _, m := range after.ModuleAccess {
			closed[m] = []uuid.UUID{uuid.Nil}
		}
		return mustJSON(closed), "property_access is malformed (the member reaches no property today); replaced by an explicit no-property scope", true
	}

	next := map[string][]uuid.UUID{}
	droppedIDs, droppedKeys := 0, 0
	for product, ids := range typed {
		if !isProduct(product) {
			droppedKeys++
			continue
		}
		if len(ids) == 0 {
			next[product] = ids
			continue
		}
		var live []uuid.UUID
		for _, id := range ids {
			if id != uuid.Nil && w.Properties[id] == tenant && !slices.Contains(live, id) {
				live = append(live, id)
			}
		}
		switch {
		case len(live) == 0 && len(ids) == 1 && ids[0] == uuid.Nil:
			next[product] = ids // already the canonical no-property scope
		case len(live) == 0:
			droppedIDs += len(ids)
			next[product] = []uuid.UUID{uuid.Nil}
		default:
			droppedIDs += len(ids) - len(live)
			next[product] = live
		}
	}
	if droppedIDs == 0 && droppedKeys == 0 {
		return raw, "", false
	}
	return mustJSON(next), fmt.Sprintf("dropped %d property id(s) that are deleted or belong to another firm and %d unknown product key(s); a list left with no live property stays restricted to nothing", droppedIDs, droppedKeys), true
}

func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err) // maps of strings to uuid lists always encode
	}
	return b
}

func jsonEqual(a, b json.RawMessage) bool {
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return string(a) == string(b)
	}
	xb, _ := json.Marshal(x)
	yb, _ := json.Marshal(y)
	return string(xb) == string(yb)
}

// ---------------------------------------------------------------------------
// Effective access: what a staff row can do, by the runtime's own rules.

// scopeOf is one product's property reach.
type scopeOf struct {
	All bool
	IDs map[uuid.UUID]bool // the live properties of the firm it reaches, when !All
}

// effective is a staff member's real reach.
type effective struct {
	Usable   bool
	Perms    map[string]bool
	Products map[string]bool
	Scope    map[string]scopeOf // per product in Products
}

// effectiveOf computes reach exactly as the APIs do: nothing unless active; a
// role's permissions only if the role is one of the four; a product only if
// module_access names it exactly; a property scope that fails closed when the
// value does not decode, is unrestricted for owners and for an empty list, and
// is otherwise the listed ids that really are live properties of this firm.
func effectiveOf(s staffState, tenant uuid.UUID, w authzWorld) effective {
	e := effective{Perms: map[string]bool{}, Products: map[string]bool{}, Scope: map[string]scopeOf{}}
	e.Usable = s.Status == platformdata.StaffActive
	if !e.Usable {
		return e
	}
	if validRole(s.Role) {
		for _, perm := range auth.RolePermissions(*s.Role) {
			e.Perms[perm] = true
		}
	}
	owner := s.Role != nil && *s.Role == platformdata.RoleOwner
	var typed map[string][]uuid.UUID
	decoded := len(s.PropertyAccess) == 0 || json.Unmarshal(s.PropertyAccess, &typed) == nil
	for _, m := range s.ModuleAccess {
		if !isProduct(m) {
			continue
		}
		e.Products[m] = true
		switch {
		case owner:
			e.Scope[m] = scopeOf{All: true}
		case !decoded:
			e.Scope[m] = scopeOf{IDs: map[uuid.UUID]bool{}}
		case len(typed[m]) == 0:
			e.Scope[m] = scopeOf{All: true}
		default:
			ids := map[uuid.UUID]bool{}
			for _, id := range typed[m] {
				if id != uuid.Nil && w.Properties[id] == tenant {
					ids[id] = true
				}
			}
			e.Scope[m] = scopeOf{IDs: ids}
		}
	}
	return e
}

// widening returns the first way after reaches further than before, or "" when
// after is within before: no new permission, product or property.
func widening(before, after effective) string {
	if after.Usable && !before.Usable {
		return "an unusable account became usable"
	}
	for perm := range after.Perms {
		if !before.Perms[perm] {
			return "gained permission " + perm
		}
	}
	for product := range after.Products {
		if !before.Products[product] {
			return "gained product " + product
		}
		b, a := before.Scope[product], after.Scope[product]
		if a.All && !b.All {
			return "gained every property of " + product
		}
		if !b.All {
			for id := range a.IDs {
				if !b.IDs[id] {
					return "gained property " + id.String()
				}
			}
		}
	}
	return ""
}
