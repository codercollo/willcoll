package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"slices"
	"testing"
	"time"

	platformdata "github.com/codercollo/willcoll/backend/internal/platform/data"
	"github.com/codercollo/willcoll/backend/internal/testenv"
	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// TestAuthzBackfillIntegration runs the backfill against messy data in real
// databases: one case of every category in docs/security/c4-spec.md C4.1.
//
// The schema already refuses some of the mess the backfill exists to repair (a
// NULL or unknown role, a staff row whose firm is gone). To seed those, the test
// connects as the scratch database's owner, drops the two constraints, seeds,
// and puts them back at the end (after deleting the rows that needed them gone).
// That is safe only because it is a throwaway database.
func TestAuthzBackfillIntegration(t *testing.T) {
	dsns := testenv.Require(t, "WILLCOLL_TEST_PLATFORM_DB_DSN", "WILLCOLL_TEST_PLATFORM_ADMIN_DB_DSN", "WILLCOLL_TEST_PLATFORM_OWNER_DSN",
		"WILLCOLL_TEST_DB_DSN", "WILLCOLL_TEST_ADMIN_DB_DSN")
	open := func(dsn string) *sql.DB {
		c, err := sql.Open("pgx", dsn)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = c.Close() })
		if err := c.Ping(); err != nil {
			t.Fatal(err)
		}
		return c
	}
	platApp, platAdmin, platOwner := open(dsns[0]), open(dsns[1]), open(dsns[2])
	rentalsApp, rentalsAdmin := open(dsns[3]), open(dsns[4])
	ctx := context.Background()
	deps := authzDeps{Platform: platAdmin, Rentals: rentalsAdmin}
	models := platformdata.NewModels(platApp, platAdmin, 5*time.Second)

	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	exec := func(db *sql.DB, q string, args ...any) {
		t.Helper()
		_, err := db.ExecContext(ctx, q, args...)
		must(err)
	}

	// --- Relax the two constraints that would stop us seeding the mess. ---
	type saved struct{ name, def string }
	var dropped []saved
	findConstraints := func(where string) []saved {
		rows, err := platOwner.QueryContext(ctx, `SELECT conname, pg_get_constraintdef(oid) FROM pg_constraint WHERE conrelid = 'staff'::regclass AND `+where)
		must(err)
		defer func() { _ = rows.Close() }()
		var out []saved
		for rows.Next() {
			var s saved
			must(rows.Scan(&s.name, &s.def))
			out = append(out, s)
		}
		must(rows.Err())
		return out
	}
	dropped = append(dropped, findConstraints(`contype = 'c' AND pg_get_constraintdef(oid) LIKE '%accountant%'`)...)
	dropped = append(dropped, findConstraints(`contype = 'f' AND confrelid = 'managers'::regclass`)...)
	if len(dropped) != 2 {
		t.Fatalf("found %d constraints to relax, want the role check and the manager foreign key: %v", len(dropped), dropped)
	}
	for _, c := range dropped {
		exec(platOwner, fmt.Sprintf(`ALTER TABLE staff DROP CONSTRAINT %q`, c.name))
	}
	exec(platOwner, `ALTER TABLE staff ALTER COLUMN role DROP NOT NULL`)

	var seeded []uuid.UUID
	var tokens []string
	t.Cleanup(func() { // runs last: puts the schema back
		for _, id := range seeded {
			_, _ = platOwner.Exec(`DELETE FROM staff WHERE id = $1`, id)
		}
		for _, tok := range tokens {
			_, _ = platOwner.Exec(`DELETE FROM sessions WHERE token = $1`, tok)
		}
		_, _ = platOwner.Exec(`ALTER TABLE staff ALTER COLUMN role SET NOT NULL`)
		for _, c := range dropped {
			if _, err := platOwner.Exec(fmt.Sprintf(`ALTER TABLE staff ADD CONSTRAINT %q %s`, c.name, c.def)); err != nil {
				t.Errorf("restoring constraint %s: %v", c.name, err)
			}
		}
	})

	// --- Two firms; rentals properties for them. ---
	newFirm := func(name string, status string) *platformdata.Manager {
		m := &platformdata.Manager{FirmName: name, Username: "u" + uuid.NewString()[:10], Email: uuid.NewString()[:8] + "@authz.example.com",
			Phone: "+254700000000", Status: platformdata.ManagerStatusPending}
		must(m.Password.Set("correct-horse-battery"))
		must(models.Managers.Insert(ctx, m))
		t.Cleanup(func() { _, _ = platOwner.Exec(`DELETE FROM managers WHERE id = $1`, m.ID) })
		exec(platOwner, `UPDATE managers SET status = $2 WHERE id = $1`, m.ID, status)
		return m
	}
	firm := newFirm("Authz Backfill Firm", "active")
	other := newFirm("Authz Other Firm", "active")
	noOwner := newFirm("Authz Firm With No Login", "active")
	exec(platOwner, `UPDATE managers SET password_hash = '\x'::bytea WHERE id = $1`, noOwner.ID)
	exec(platOwner, `INSERT INTO manager_modules (manager_id, product, status) VALUES ($1, 'rentals', 'active'), ($2, 'rentals', 'active')`, firm.ID, other.ID)

	tenantTx := func(tenant uuid.UUID, fn func(tx *sql.Tx)) {
		tx, err := rentalsApp.BeginTx(ctx, nil)
		must(err)
		defer func() { _ = tx.Rollback() }()
		_, err = tx.Exec(`SELECT set_config('app.tenant_id', $1, true), set_config('app.property_scope', 'all', true)`, tenant.String())
		must(err)
		fn(tx)
		must(tx.Commit())
	}
	newProperty := func(tenant uuid.UUID, deleted bool) uuid.UUID {
		landlord, prop := uuid.New(), uuid.New()
		tenantTx(tenant, func(tx *sql.Tx) {
			_, err := tx.Exec(`INSERT INTO landlords (id, tenant_id, name, phone) VALUES ($1, $2, 'L', '+254711111111')`, landlord, tenant)
			must(err)
			_, err = tx.Exec(`INSERT INTO properties (id, tenant_id, landlord_id, name, location, slug) VALUES ($1, $2, $3, 'P', 'Nairobi', $4)`,
				prop, tenant, landlord, "authz-"+prop.String())
			must(err)
			if deleted {
				_, err = tx.Exec(`UPDATE properties SET deleted_at = now() WHERE id = $1`, prop)
				must(err)
			}
		})
		return prop
	}
	propA, propB := newProperty(firm.ID, false), newProperty(firm.ID, false)
	propGone := newProperty(firm.ID, true)
	propOther := newProperty(other.ID, false)

	// --- Seed one staff row per category. ---
	seed := func(manager uuid.UUID, role *string, modules, access, status string) uuid.UUID {
		id := uuid.New()
		seeded = append(seeded, id)
		exec(platOwner, `INSERT INTO staff (id, manager_id, name, email, password_hash, role, module_access, property_access, status)
			VALUES ($1, $2, 'S', $3, '\x01'::bytea, $4, $5::text[], $6::jsonb, $7)`,
			id, manager, id.String()+"@authz.example.com", role, modules, access, status)
		return id
	}
	addSession := func(staff uuid.UUID) {
		sm := sessionManager(platApp)
		sc, err := sm.Load(ctx, "")
		must(err)
		sm.Put(sc, sessionKeyType, principalStaff)
		sm.Put(sc, sessionKeyID, staff.String())
		token, _, err := sm.Commit(sc)
		must(err)
		tokens = append(tokens, token)
	}
	access := func(rentals ...uuid.UUID) string {
		b, _ := json.Marshal(map[string][]uuid.UUID{"rentals": rentals})
		return string(b)
	}
	officer, viewer, owner, root := "officer", "viewer", "owner", "root"

	roleNull := seed(firm.ID, nil, `{rentals}`, `{}`, "active")
	roleBad := seed(firm.ID, &root, `{rentals}`, `{}`, "invited")
	modMessy := seed(firm.ID, &officer, `{rentals,rentals,bogus,Rentals}`, `{}`, "active")
	modCase := seed(firm.ID, &officer, `{Rentals}`, `{}`, "active")
	modLapsed := seed(firm.ID, &viewer, `{rentals,waste}`, `{}`, "active")
	propPartial := seed(firm.ID, &officer, `{rentals}`, access(propA, propGone, propOther), "active")
	propNone := seed(firm.ID, &officer, `{rentals}`, access(propGone, propOther), "active")
	propBroken := seed(firm.ID, &officer, `{rentals}`, `{"rentals":"x"}`, "active")
	propClean := seed(firm.ID, &officer, `{rentals}`, access(propA, propB), "active")
	ownerJunk := seed(firm.ID, &owner, `{rentals}`, access(propGone), "active")
	inactive := seed(firm.ID, &officer, `{rentals}`, `{}`, "disabled")
	addSession(inactive)
	addSession(inactive)
	activeWithSession := seed(firm.ID, &officer, `{rentals}`, `{}`, "active")
	addSession(activeWithSession)
	orphan := seed(uuid.New(), &officer, `{rentals}`, `{}`, "active")
	addSession(orphan)
	removed := uuid.New() // a session for a staff row that no longer exists
	addSession(removed)

	snapshot := func() map[uuid.UUID]effective {
		rows, w, _, _, err := loadAuthzWorld(ctx, deps)
		must(err)
		out := map[uuid.UUID]effective{}
		for _, r := range rows {
			out[r.ID] = effectiveOf(r.staffState, r.ManagerID, w)
		}
		return out
	}
	stateOf := func(id uuid.UUID) staffRow {
		rows, _, _, _, err := loadAuthzWorld(ctx, deps)
		must(err)
		for _, r := range rows {
			if r.ID == id {
				return r
			}
		}
		t.Fatalf("staff %s not found", id)
		return staffRow{}
	}
	liveSessions := func() map[uuid.UUID]int {
		_, w, removedSessions, _, err := loadAuthzWorld(ctx, deps)
		must(err)
		out := map[uuid.UUID]int{}
		for id, n := range w.LiveSessions {
			out[id] = n
		}
		for id, n := range removedSessions {
			out[id] += n
		}
		return out
	}
	countAudit := func(category string) (n int) {
		must(platOwner.QueryRowContext(ctx, `SELECT count(*) FROM audit_log WHERE actor_type = 'system' AND actor_name = $1 AND action = $2 AND manager_id = $3`,
			authzActorName, "authz_backfill."+category, firm.ID).Scan(&n))
		return n
	}

	before := snapshot()

	// --- Dry run: reports, writes nothing. ---
	dry, err := authzBackfill(ctx, deps, false)
	must(err)
	has := func(rep *authzReport, cat string, id uuid.UUID) bool { return slices.Contains(rep.Changes[cat], id) }
	for cat, want := range map[string][]uuid.UUID{
		catRole:             {roleNull, roleBad},
		catModuleAccess:     {modMessy, modCase},
		catPropertyAccess:   {propPartial, propNone, propBroken},
		catSessionsInactive: {inactive},
		catOrphanStaff:      {orphan},
	} {
		for _, id := range want {
			if !has(dry, cat, id) {
				t.Errorf("dry run: %s does not list %s", cat, id)
			}
		}
	}
	if !slices.Contains(dry.Findings[catModuleLapsed], modLapsed) {
		t.Errorf("dry run does not report the lapsed module of %s", modLapsed)
	}
	if !slices.Contains(dry.Findings[catFirmNoOwner], noOwner.ID) {
		t.Errorf("dry run does not report the firm login without an owner")
	}
	if dry.RemovedSessions[removed] != 1 {
		t.Errorf("dry run: sessions of the removed staff = %v, want 1", dry.RemovedSessions)
	}
	for _, id := range []uuid.UUID{propClean, ownerJunk, activeWithSession} {
		for cat, list := range dry.Changes {
			if slices.Contains(list, id) {
				t.Errorf("dry run plans a %s change for %s, which is already fine", cat, id)
			}
		}
	}
	if got := countAudit(catRole); got != 0 || stateOf(roleNull).Role != nil {
		t.Fatalf("the dry run wrote something (audit rows %d, role %v)", got, stateOf(roleNull).Role)
	}

	// --- Apply. ---
	applied, err := authzBackfill(ctx, deps, true)
	must(err)
	if applied.Applied == 0 || len(applied.Conflicts) != 0 {
		t.Fatalf("apply: applied %d, conflicts %v", applied.Applied, applied.Conflicts)
	}

	// Everything is normalized.
	if r := stateOf(roleNull); r.Role == nil || *r.Role != viewer || r.Status != "disabled" {
		t.Errorf("NULL role -> %v / %s, want viewer and disabled", r.Role, r.Status)
	}
	if r := stateOf(roleBad); r.Role == nil || *r.Role != viewer || r.Status != "disabled" {
		t.Errorf("invalid role -> %v / %s, want viewer and disabled", r.Role, r.Status)
	}
	if r := stateOf(modMessy); !slices.Equal(r.ModuleAccess, []string{"rentals"}) {
		t.Errorf("messy module_access -> %v, want [rentals]", r.ModuleAccess)
	}
	if r := stateOf(modCase); len(r.ModuleAccess) != 0 {
		t.Errorf("wrong-case module_access -> %v, want [] (lowercasing would grant)", r.ModuleAccess)
	}
	if r := stateOf(modLapsed); !slices.Equal(r.ModuleAccess, []string{"rentals", "waste"}) {
		t.Errorf("lapsed module was changed: %v (report only)", r.ModuleAccess)
	}
	if r := stateOf(propPartial); !jsonEqual(r.PropertyAccess, json.RawMessage(access(propA))) {
		t.Errorf("partial prune -> %s, want only property A", r.PropertyAccess)
	}
	for _, id := range []uuid.UUID{propNone, propBroken} {
		if r := stateOf(id); !jsonEqual(r.PropertyAccess, json.RawMessage(access(uuid.Nil))) {
			t.Errorf("fully invalid scope %s -> %s, want the no-property scope (never 'all properties')", id, r.PropertyAccess)
		}
	}
	if r := stateOf(orphan); r.Status != "disabled" {
		t.Errorf("orphan status = %s, want disabled", r.Status)
	}
	if r := stateOf(propClean); !jsonEqual(r.PropertyAccess, json.RawMessage(access(propA, propB))) {
		t.Errorf("a clean row was changed: %s", r.PropertyAccess)
	}
	live := liveSessions()
	if live[inactive] != 0 || live[orphan] != 0 || live[removed] != 0 {
		t.Errorf("sessions left after apply: inactive %d orphan %d removed %d", live[inactive], live[orphan], live[removed])
	}
	if live[activeWithSession] != 1 {
		t.Errorf("an active member's session was revoked (have %d)", live[activeWithSession])
	}

	// One audit row per change, as the system actor, with a reason.
	for cat, want := range map[string]int{catRole: 2, catModuleAccess: 2, catPropertyAccess: 3, catSessionsInactive: 1} {
		if got := countAudit(cat); got != want {
			t.Errorf("audit rows for %s = %d, want %d", cat, got, want)
		}
	}
	var noReason int
	must(platOwner.QueryRowContext(ctx, `SELECT count(*) FROM audit_log WHERE actor_name = $1 AND (summary = '' OR actor_id IS NOT NULL OR staff_id IS NOT NULL)`, authzActorName).Scan(&noReason))
	if noReason != 0 {
		t.Errorf("%d backfill audit rows have no reason or a named actor", noReason)
	}
	var orphanAudit int
	must(platOwner.QueryRowContext(ctx, `SELECT count(*) FROM audit_log WHERE entity_id = $1`, orphan).Scan(&orphanAudit))
	if orphanAudit != 0 {
		t.Errorf("an orphan has %d audit rows; it has no firm to attach them to", orphanAudit)
	}

	// No principal gained any permission, product or property.
	after := snapshot()
	for id, b := range before {
		a, ok := after[id]
		if !ok {
			continue
		}
		if why := widening(b, a); why != "" {
			t.Errorf("staff %s: %s", id, why)
		}
	}

	// A second apply changes nothing, and writes nothing.
	auditRows := func() (n int) {
		must(platOwner.QueryRowContext(ctx, `SELECT count(*) FROM audit_log WHERE actor_name = $1`, authzActorName).Scan(&n))
		return n
	}
	rowsBefore := auditRows()
	again, err := authzBackfill(ctx, deps, true)
	must(err)
	if again.TotalChanges() != 0 || again.Applied != 0 {
		t.Errorf("second apply: %d changes, %d applied; want 0 (%v)", again.TotalChanges(), again.Applied, again.Changes)
	}
	if auditRows() != rowsBefore {
		t.Errorf("second apply wrote audit rows")
	}
	if dry2, err := authzBackfill(ctx, deps, false); err != nil || dry2.TotalChanges() != 0 {
		t.Errorf("dry run after apply: %v / %d changes, want 0", err, dry2.TotalChanges())
	}
}
