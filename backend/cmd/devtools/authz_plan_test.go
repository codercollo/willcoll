package main

import (
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"slices"
	"strings"
	"testing"

	platformdata "github.com/codercollo/willcoll/backend/internal/platform/data"
	"github.com/google/uuid"
)

func str(s string) *string { return &s }

// testWorld: one firm that holds rentals only, with two live properties, plus a
// second firm with one property (the "other firm" case).
type testWorld struct {
	w               authzWorld
	firm, otherFirm uuid.UUID
	propA, propB    uuid.UUID
	otherFirmProp   uuid.UUID
	deletedProp     uuid.UUID // exists nowhere: archived or hard-deleted
}

func newTestWorld() testWorld {
	t := testWorld{
		firm: uuid.New(), otherFirm: uuid.New(), propA: uuid.New(), propB: uuid.New(),
		otherFirmProp: uuid.New(), deletedProp: uuid.New(),
	}
	t.w = authzWorld{
		Managers:     map[uuid.UUID]bool{t.firm: true, t.otherFirm: true},
		HeldModules:  map[uuid.UUID]map[string]bool{t.firm: {"rentals": true}, t.otherFirm: {"rentals": true}},
		Properties:   map[uuid.UUID]uuid.UUID{t.propA: t.firm, t.propB: t.firm, t.otherFirmProp: t.otherFirm},
		LiveSessions: map[uuid.UUID]int{},
	}
	return t
}

func (tw testWorld) row(role *string, modules []string, access string, status string) staffRow {
	return staffRow{
		ID: uuid.New(), ManagerID: tw.firm, Version: 1,
		staffState: staffState{Role: role, ModuleAccess: modules, PropertyAccess: json.RawMessage(access), Status: status},
	}
}

func hasChange(p staffPlan, cat string) bool {
	for _, c := range p.Changes {
		if c.Category == cat {
			return true
		}
	}
	return false
}

func TestPlanRoleNullOrInvalidBecomesViewerAndStaysInactive(t *testing.T) {
	tw := newTestWorld()
	for _, role := range []*string{nil, str("superuser"), str("")} {
		r := tw.row(role, []string{"rentals"}, `{}`, "active")
		p := planStaff(r, tw.w)
		if !hasChange(p, catRole) || p.After.Role == nil || *p.After.Role != platformdata.RoleViewer {
			t.Fatalf("role %v: plan = %+v, want viewer", role, p)
		}
		if p.After.Status != platformdata.StaffDisabled {
			t.Errorf("role %v: status %s, want disabled (an unknown role grants nothing today; viewer would widen)", role, p.After.Status)
		}
	}
	// A valid role is untouched.
	if p := planStaff(tw.row(str("officer"), []string{"rentals"}, `{}`, "active"), tw.w); len(p.Changes) != 0 {
		t.Errorf("a valid row planned changes: %+v", p.Changes)
	}
}

func TestPlanModuleAccess(t *testing.T) {
	tw := newTestWorld()

	// Wrong case is DROPPED, not lowercased: "Rentals" opens nothing today.
	p := planStaff(tw.row(str("officer"), []string{"Rentals", "waste "}, `{}`, "active"), tw.w)
	if !hasChange(p, catModuleAccess) || len(p.After.ModuleAccess) != 0 {
		t.Errorf("case variants: after = %v, want none (lowercasing would grant access)", p.After.ModuleAccess)
	}
	// Duplicates and unknown entries go; real ones stay, in order.
	p = planStaff(tw.row(str("officer"), []string{"rentals", "rentals", "bogus", "rentals"}, `{}`, "active"), tw.w)
	if !slices.Equal(p.After.ModuleAccess, []string{"rentals"}) || !hasChange(p, catModuleAccess) {
		t.Errorf("dedupe: after = %v", p.After.ModuleAccess)
	}
	// A lapsed module (known product, firm no longer holds it) is reported and KEPT.
	p = planStaff(tw.row(str("officer"), []string{"rentals", "estates"}, `{}`, "active"), tw.w)
	if len(p.Findings) != 1 || p.Findings[0].Category != catModuleLapsed || !slices.Equal(p.After.ModuleAccess, []string{"rentals", "estates"}) {
		t.Errorf("lapsed: findings %+v after %v, want reported only", p.Findings, p.After.ModuleAccess)
	}
}

func TestPlanPropertyAccess(t *testing.T) {
	tw := newTestWorld()
	access := func(rentals ...uuid.UUID) string {
		b, _ := json.Marshal(map[string][]uuid.UUID{"rentals": rentals})
		return string(b)
	}
	officer := str("officer")

	// Deleted and other-firm ids are dropped; the live one stays.
	p := planStaff(tw.row(officer, []string{"rentals"}, access(tw.propA, tw.deletedProp, tw.otherFirmProp), "active"), tw.w)
	if !hasChange(p, catPropertyAccess) || string(p.After.PropertyAccess) != access(tw.propA) {
		t.Errorf("partial prune: %s", p.After.PropertyAccess)
	}

	// All ids invalid must NOT become empty (= every property): it stays restricted to nothing.
	p = planStaff(tw.row(officer, []string{"rentals"}, access(tw.deletedProp, tw.otherFirmProp), "active"), tw.w)
	if string(p.After.PropertyAccess) != access(uuid.Nil) {
		t.Errorf("all invalid: %s, want the nil-uuid no-property scope", p.After.PropertyAccess)
	}
	// ... and that canonical value is then left alone (idempotent).
	if p2 := planStaff(tw.row(officer, []string{"rentals"}, access(uuid.Nil), "active"), tw.w); hasChange(p2, catPropertyAccess) {
		t.Errorf("the no-property scope was rewritten: %+v", p2.Changes)
	}

	// Malformed JSON shape: fail-closed today, so fail-closed after.
	for _, bad := range []string{`{"rentals":"nope"}`, `{"rentals":["not-a-uuid"]}`, `{"rentals":[1,2]}`} {
		p = planStaff(tw.row(officer, []string{"rentals"}, bad, "active"), tw.w)
		if !hasChange(p, catPropertyAccess) || string(p.After.PropertyAccess) != access(uuid.Nil) {
			t.Errorf("malformed %s -> %s", bad, p.After.PropertyAccess)
		}
	}

	// Owners are never restricted, so their value is left alone.
	if p = planStaff(tw.row(str("owner"), []string{"rentals"}, access(tw.deletedProp), "active"), tw.w); hasChange(p, catPropertyAccess) {
		t.Errorf("owner property_access was touched")
	}
}

func TestPlanSessionsAndOrphans(t *testing.T) {
	tw := newTestWorld()

	disabled := tw.row(str("officer"), []string{"rentals"}, `{}`, "disabled")
	tw.w.LiveSessions[disabled.ID] = 2
	p := planStaff(disabled, tw.w)
	if !p.RevokeSessions || !hasChange(p, catSessionsInactive) {
		t.Errorf("disabled staff with sessions: %+v", p)
	}

	active := tw.row(str("officer"), []string{"rentals"}, `{}`, "active")
	tw.w.LiveSessions[active.ID] = 1
	if p = planStaff(active, tw.w); p.RevokeSessions || len(p.Changes) != 0 {
		t.Errorf("active staff must keep their sessions: %+v", p)
	}

	orphan := tw.row(str("officer"), []string{"rentals"}, `{}`, "active")
	orphan.ManagerID = uuid.New()
	tw.w.LiveSessions[orphan.ID] = 3
	p = planStaff(orphan, tw.w)
	if p.After.Status != platformdata.StaffDisabled || !p.RevokeSessions || !hasChange(p, catOrphanStaff) {
		t.Errorf("orphan: %+v", p)
	}
}

// applyInMemory is what apply does to a row, without a database.
func applyInMemory(r staffRow, p staffPlan) staffRow {
	r.staffState = p.After
	return r
}

func TestPlanNeverWidensAndSecondRunChangesNothing(t *testing.T) {
	tw := newTestWorld()
	rng := rand.New(rand.NewPCG(7, 11))

	roles := []*string{nil, str("owner"), str("accountant"), str("officer"), str("viewer"), str("root"), str("")}
	statuses := []string{"active", "invited", "disabled"}
	modules := []string{"rentals", "estates", "waste", "Rentals", "WASTE ", "bogus", "", "rentals"}
	ids := []uuid.UUID{tw.propA, tw.propB, tw.otherFirmProp, tw.deletedProp, uuid.Nil}
	pick := func(n int) []int { return rng.Perm(n)[:rng.IntN(n+1)] }

	for i := range 4000 {
		var mods []string
		for _, k := range pick(len(modules)) {
			mods = append(mods, modules[k])
		}
		if rng.IntN(5) == 0 && len(mods) > 0 {
			mods = append(mods, mods[0])
		}
		var access string
		switch rng.IntN(6) {
		case 0:
			access = `{}`
		case 1:
			access = `{"rentals":"x"}`
		case 2:
			access = `{"rentals":["zzz"],"estates":[]}`
		case 3:
			access = `{"mystery":["` + tw.propA.String() + `"]}`
		default:
			m := map[string][]uuid.UUID{}
			for _, product := range []string{"rentals", "estates", "waste"} {
				if rng.IntN(2) == 0 {
					continue
				}
				var l []uuid.UUID
				for _, k := range pick(len(ids)) {
					l = append(l, ids[k])
				}
				m[product] = l
			}
			b, _ := json.Marshal(m)
			access = string(b)
		}
		r := tw.row(roles[rng.IntN(len(roles))], mods, access, statuses[rng.IntN(len(statuses))])
		if rng.IntN(7) == 0 {
			r.ManagerID = uuid.New() // orphan
		}
		if rng.IntN(3) == 0 {
			tw.w.LiveSessions[r.ID] = 1 + rng.IntN(3)
		}

		before := effectiveOf(r.staffState, r.ManagerID, tw.w)
		p := planStaff(r, tw.w)
		after := effectiveOf(p.After, r.ManagerID, tw.w)
		if why := widening(before, after); why != "" {
			t.Fatalf("case %d: the plan WIDENED access (%s)\nbefore: %+v\nafter:  %+v", i, why, r.staffState, p.After)
		}

		// A second run over the applied row changes nothing (sessions are gone once applied).
		next := applyInMemory(r, p)
		if p.RevokeSessions {
			delete(tw.w.LiveSessions, r.ID)
		}
		if p2 := planStaff(next, tw.w); len(p2.Changes) != 0 {
			t.Fatalf("case %d: a second run still changes %s\nafter first run: %+v", i, describeChanges(p2.Changes), next.staffState)
		}
	}
}

func describeChanges(cs []authzChange) string {
	var parts []string
	for _, c := range cs {
		parts = append(parts, fmt.Sprintf("%s (%s)", c.Category, c.Reason))
	}
	return strings.Join(parts, "; ")
}

// widening itself must catch widening, or the property test above proves nothing.
func TestWideningCatchesEveryKindOfGain(t *testing.T) {
	tw := newTestWorld()
	base := staffState{Role: str("officer"), Status: "active", ModuleAccess: []string{"rentals"}, PropertyAccess: json.RawMessage(fmt.Sprintf(`{"rentals":[%q]}`, tw.propA))}
	mutate := map[string]func(s staffState) staffState{
		"role up":       func(s staffState) staffState { s.Role = str("owner"); return s },
		"extra product": func(s staffState) staffState { s.ModuleAccess = []string{"rentals", "estates"}; return s },
		"scope cleared": func(s staffState) staffState { s.PropertyAccess = json.RawMessage(`{}`); return s },
		"another place": func(s staffState) staffState {
			s.PropertyAccess = json.RawMessage(fmt.Sprintf(`{"rentals":[%q]}`, tw.propB))
			return s
		},
		"lowercased case": func(s staffState) staffState { s.ModuleAccess = []string{"rentals", "waste"}; return s },
	}
	for name, f := range mutate {
		before := base
		if name == "lowercased case" {
			before.ModuleAccess = []string{"rentals", "Waste"}
		}
		if widening(effectiveOf(before, tw.firm, tw.w), effectiveOf(f(before), tw.firm, tw.w)) == "" {
			t.Errorf("%s: widening() missed it", name)
		}
	}
	// Re-enabling is the one that needs the status flip from disabled -> active.
	dis, act := base, base
	dis.Status = "disabled"
	if widening(effectiveOf(dis, tw.firm, tw.w), effectiveOf(act, tw.firm, tw.w)) == "" {
		t.Error("re-enabling a disabled account was not flagged")
	}
}

func TestParsePGTextArray(t *testing.T) {
	cases := map[string][]string{
		`{}`:                      {},
		`{rentals}`:               {"rentals"},
		`{rentals,estates}`:       {"rentals", "estates"},
		`{Rentals,"waste "}`:      {"Rentals", "waste "},
		`{"a,b",c}`:               {"a,b", "c"},
		`{"quo\"te"}`:             {`quo"te`},
		`{rentals,rentals,bogus}`: {"rentals", "rentals", "bogus"},
	}
	for in, want := range cases {
		if got := parsePGTextArray(in); !slices.Equal(got, want) {
			t.Errorf("parsePGTextArray(%s) = %q, want %q", in, got, want)
		}
	}
}
