package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"slices"
	"strings"

	"github.com/alexedwards/scs/postgresstore"
	"github.com/alexedwards/scs/v2"
	"github.com/google/uuid"
)

// authz-backfill (docs/security/c4-spec.md C4.1) puts every existing staff row
// into a valid, least-privilege state without changing what any real user can do
// today: it only ever narrows or keeps access (see authz_plan.go for the rule and
// the planner).
//
//	devtools authz-backfill --dry-run    reports counts and ids, writes nothing
//	devtools authz-backfill --apply      applies exactly what the dry run reports
//
// It is a devtool and not a migration on purpose: a migration runs the moment a
// release is deployed, with nobody having read the dry-run counts, and the
// property check needs a second database (willcoll_rentals) no platform migration
// can see. It runs in any ENV (like backfill-platform) and is safe to re-run: a
// second --apply changes nothing.
//
// Environment: PLATFORM_DSN (willcoll_platform_admin) and RENTALS_ADMIN_DSN
// (willcoll_rentals' admin role, read-only use: live property ids).

const (
	authzActorName = "authz-backfill"
	sessionKeyType = "principal_type"
	sessionKeyID   = "principal_id"
	principalStaff = "staff"
)

// authzDeps are the two databases the backfill reads and writes.
type authzDeps struct {
	Platform *sql.DB // willcoll_platform_admin: BYPASSRLS; UPDATE staff, INSERT audit_log (000025), DELETE sessions
	Rentals  *sql.DB // willcoll_rentals admin: SELECT properties
}

// authzReport is what a run found and (with apply) did.
type authzReport struct {
	Scanned int
	Firms   int
	// Changes: category -> staff ids. One entry per audit row.
	Changes map[string][]uuid.UUID
	// Findings: report-only category -> ids (staff ids; firm ids for firm_login_without_owner).
	Findings map[string][]uuid.UUID
	// RemovedSessions: staff id -> live sessions of a staff member whose row no
	// longer exists. No audit row can name them (no firm to attach it to).
	RemovedSessions map[uuid.UUID]int
	// Applied counts changes written; Conflicts are staff rows changed by
	// someone else mid-run (skipped; a re-run picks them up).
	Applied   int
	Conflicts []uuid.UUID
}

// TotalChanges is the number of changes (dry run: would be made).
func (r *authzReport) TotalChanges() int {
	n := len(r.RemovedSessions)
	for _, ids := range r.Changes {
		n += len(ids)
	}
	return n
}

func newAuthzReport() *authzReport {
	return &authzReport{Changes: map[string][]uuid.UUID{}, Findings: map[string][]uuid.UUID{}, RemovedSessions: map[uuid.UUID]int{}}
}

func runAuthzBackfill(args []string) {
	fs := flag.NewFlagSet("authz-backfill", flag.ExitOnError)
	dry := fs.Bool("dry-run", false, "report counts and ids; change nothing")
	apply := fs.Bool("apply", false, "apply the changes the dry run reports")
	_ = fs.Parse(args)
	if *dry == *apply {
		log.Fatal("usage: devtools authz-backfill --dry-run | --apply (exactly one)")
	}
	platformDSN, rentalsDSN := os.Getenv("PLATFORM_DSN"), os.Getenv("RENTALS_ADMIN_DSN")
	if platformDSN == "" || rentalsDSN == "" {
		log.Fatal("PLATFORM_DSN (willcoll_platform_admin) and RENTALS_ADMIN_DSN (willcoll_rentals admin, read-only use) must both be set: property ids are checked against the rentals database")
	}
	deps := authzDeps{Platform: mustOpen(platformDSN), Rentals: mustOpen(rentalsDSN)}
	defer func() { _ = deps.Platform.Close(); _ = deps.Rentals.Close() }()

	rep, err := authzBackfill(context.Background(), deps, *apply)
	if err != nil {
		log.Fatal(err)
	}
	rep.print(os.Stdout, *apply)
}

// authzBackfill scans, plans and (with apply) writes. The report is the same in
// both modes: with apply, Applied says how much was written.
func authzBackfill(ctx context.Context, deps authzDeps, apply bool) (*authzReport, error) {
	rows, w, removed, noOwner, err := loadAuthzWorld(ctx, deps)
	if err != nil {
		return nil, err
	}
	rep := newAuthzReport()
	rep.Scanned, rep.Firms = len(rows), len(w.Managers)
	rep.RemovedSessions = removed
	if len(noOwner) > 0 {
		rep.Findings[catFirmNoOwner] = noOwner
	}

	var plans []staffPlan
	for _, r := range rows {
		p := planStaff(r, w)
		for _, c := range p.Changes {
			rep.Changes[c.Category] = append(rep.Changes[c.Category], c.StaffID)
		}
		for _, f := range p.Findings {
			rep.Findings[f.Category] = append(rep.Findings[f.Category], f.StaffID)
		}
		if len(p.Changes) > 0 {
			plans = append(plans, p)
		}
	}
	if !apply {
		return rep, nil
	}

	sm := sessionManager(deps.Platform)
	revoke := map[uuid.UUID]bool{}
	for id := range removed {
		revoke[id] = true
	}
	for _, p := range plans {
		conflict, err := applyStaffPlan(ctx, deps.Platform, p, w)
		if err != nil {
			return rep, fmt.Errorf("staff %s: %w", p.Row.ID, err)
		}
		if conflict {
			rep.Conflicts = append(rep.Conflicts, p.Row.ID)
			continue
		}
		rep.Applied += len(p.Changes)
		if p.RevokeSessions {
			revoke[p.Row.ID] = true
		}
	}
	revoked, err := destroySessions(ctx, sm, revoke)
	if err != nil {
		return rep, fmt.Errorf("revoking sessions: %w", err)
	}
	rep.Applied += len(removed)
	// The session-revocation audit rows are written after the sessions are gone,
	// with the number really destroyed, so the trail never claims more than was done.
	for _, p := range plans {
		for _, c := range p.Changes {
			if c.Category == catSessionsInactive && w.Managers[c.ManagerID] && revoke[p.Row.ID] {
				if err := insertAuditTx(ctx, deps.Platform, c.ManagerID, p.Row.ID, c.Category,
					fmt.Sprintf("revoked %d live session(s) of a staff member who is %s", revoked[p.Row.ID], p.After.Status)); err != nil {
					return rep, err
				}
			}
		}
	}
	return rep, nil
}

// loadAuthzWorld reads everything the plan needs.
func loadAuthzWorld(ctx context.Context, deps authzDeps) (rows []staffRow, w authzWorld, removed map[uuid.UUID]int, noOwner []uuid.UUID, err error) {
	w = authzWorld{
		Managers: map[uuid.UUID]bool{}, HeldModules: map[uuid.UUID]map[string]bool{},
		Properties: map[uuid.UUID]uuid.UUID{}, LiveSessions: map[uuid.UUID]int{},
	}
	removed = map[uuid.UUID]int{}

	if err = queryIDs(ctx, deps.Platform, `SELECT id FROM managers`, func(id uuid.UUID) { w.Managers[id] = true }); err != nil {
		return
	}
	// A firm login is the implicit owner: it needs no staff row, but it must be
	// able to sign in. Active firms with no email or no password hash cannot.
	if err = queryIDs(ctx, deps.Platform, `SELECT id FROM managers WHERE status = 'active' AND (btrim(email::text) = '' OR octet_length(password_hash) = 0) ORDER BY id`,
		func(id uuid.UUID) { noOwner = append(noOwner, id) }); err != nil {
		return
	}

	err = queryEach(ctx, deps.Platform, `SELECT manager_id, product FROM manager_modules WHERE status IN ('trialing', 'active')`, func(r *sql.Rows) error {
		var id uuid.UUID
		var product string
		if err := r.Scan(&id, &product); err != nil {
			return err
		}
		if w.HeldModules[id] == nil {
			w.HeldModules[id] = map[string]bool{}
		}
		w.HeldModules[id][product] = true
		return nil
	})
	if err != nil {
		return
	}

	err = queryEach(ctx, deps.Rentals, `SELECT id, tenant_id FROM properties WHERE deleted_at IS NULL`, func(r *sql.Rows) error {
		var id, tenant uuid.UUID
		if err := r.Scan(&id, &tenant); err != nil {
			return err
		}
		w.Properties[id] = tenant
		return nil
	})
	if err != nil {
		return
	}

	err = queryEach(ctx, deps.Platform, `
		SELECT id, manager_id, version, role, module_access::text, property_access::text, status
		FROM staff ORDER BY id`, func(r *sql.Rows) error {
		var s staffRow
		var modules, access string
		if err := r.Scan(&s.ID, &s.ManagerID, &s.Version, &s.Role, &modules, &access, &s.Status); err != nil {
			return err
		}
		s.ModuleAccess = parsePGTextArray(modules)
		s.PropertyAccess = json.RawMessage(access)
		rows = append(rows, s)
		return nil
	})
	if err != nil {
		return
	}

	exists := map[uuid.UUID]bool{}
	for _, r := range rows {
		exists[r.ID] = true
	}
	err = eachStaffSession(ctx, sessionManager(deps.Platform), func(id uuid.UUID) {
		if exists[id] {
			w.LiveSessions[id]++
		} else {
			removed[id]++
		}
	})
	return
}

// queryEach runs query and calls scan once per row.
func queryEach(ctx context.Context, db *sql.DB, query string, scan func(*sql.Rows) error) error {
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		if err := scan(rows); err != nil {
			return err
		}
	}
	return rows.Err()
}

func queryIDs(ctx context.Context, db *sql.DB, query string, each func(uuid.UUID)) error {
	return queryEach(ctx, db, query, func(r *sql.Rows) error {
		var id uuid.UUID
		if err := r.Scan(&id); err != nil {
			return err
		}
		each(id)
		return nil
	})
}

// parsePGTextArray reads a Postgres text[] rendered as text ({a,b} or
// {"A b","c"}). module_access holds product names, so this only needs to
// handle plain and double-quoted elements.
func parsePGTextArray(s string) []string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(strings.TrimSuffix(s, "}"), "{")
	out := []string{}
	if s == "" {
		return out
	}
	var cur strings.Builder
	inQuote, escaped, quoted := false, false, false
	flush := func() {
		v := cur.String()
		if !quoted && v == "NULL" {
			v = ""
		}
		out = append(out, v)
		cur.Reset()
		quoted = false
	}
	for _, r := range s {
		switch {
		case escaped:
			cur.WriteRune(r)
			escaped = false
		case r == '\\' && inQuote:
			escaped = true
		case r == '"':
			inQuote, quoted = !inQuote, true
		case r == ',' && !inQuote:
			flush()
		default:
			cur.WriteRune(r)
		}
	}
	flush()
	return out
}

// applyStaffPlan writes one staff row's changes and their audit rows in a
// single transaction. conflict is true when the row changed since it was read
// (optimistic lock on version): nothing is written and a re-run retries it.
func applyStaffPlan(ctx context.Context, db *sql.DB, p staffPlan, w authzWorld) (conflict bool, err error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()

	if p.stateChanged() {
		modules, _ := json.Marshal(p.After.ModuleAccess)
		res, err := tx.ExecContext(ctx, `
			UPDATE staff
			SET role = $2,
			    module_access = ARRAY(SELECT jsonb_array_elements_text($3::jsonb)),
			    property_access = $4::jsonb,
			    status = $5,
			    version = version + 1
			WHERE id = $1 AND version = $6`,
			p.Row.ID, p.After.Role, string(modules), string(p.After.PropertyAccess), p.After.Status, p.Row.Version)
		if err != nil {
			return false, err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return true, nil
		}
	}
	if w.Managers[p.Row.ManagerID] {
		for _, c := range p.Changes {
			if c.Category == catSessionsInactive {
				continue // written after the sessions are destroyed
			}
			if err := insertAuditTx(ctx, tx, c.ManagerID, c.StaffID, c.Category, c.Reason); err != nil {
				return false, err
			}
		}
	}
	return false, tx.Commit()
}

type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// insertAuditTx writes one system-actor audit row: actor_type=system,
// actor_name=authz-backfill, entity = the staff row. The summary is a rule name
// and counts: no names, phones or raw values.
func insertAuditTx(ctx context.Context, e execer, manager, staff uuid.UUID, category, summary string) error {
	_, err := e.ExecContext(ctx, `
		INSERT INTO audit_log (manager_id, actor_type, actor_name, action, entity_type, entity_id, summary)
		VALUES ($1, 'system', $2, $3, 'staff', $4, $5)`,
		manager, authzActorName, "authz_backfill."+category, staff, summary)
	return err
}

// ---------------------------------------------------------------------------
// Sessions. SCS stores them as opaque blobs, so they are walked with Iterate
// (the same way platform-api revokes a member's sessions).

func sessionManager(db *sql.DB) *scs.SessionManager {
	sm := scs.New()
	sm.Store = postgresstore.New(db)
	return sm
}

// eachStaffSession calls fn with the staff id of every live staff session.
func eachStaffSession(ctx context.Context, sm *scs.SessionManager, fn func(uuid.UUID)) error {
	return sm.Iterate(ctx, func(sc context.Context) error {
		if sm.GetString(sc, sessionKeyType) != principalStaff {
			return nil
		}
		id, err := uuid.Parse(sm.GetString(sc, sessionKeyID))
		if err != nil {
			return nil // not a session we can attribute to a staff member
		}
		fn(id)
		return nil
	})
}

// destroySessions deletes every live session of the given staff ids and
// returns how many each had.
func destroySessions(ctx context.Context, sm *scs.SessionManager, ids map[uuid.UUID]bool) (map[uuid.UUID]int, error) {
	done := map[uuid.UUID]int{}
	if len(ids) == 0 {
		return done, nil
	}
	err := sm.Iterate(ctx, func(sc context.Context) error {
		if sm.GetString(sc, sessionKeyType) != principalStaff {
			return nil
		}
		id, perr := uuid.Parse(sm.GetString(sc, sessionKeyID))
		if perr != nil || !ids[id] {
			return nil
		}
		done[id]++
		return sm.Destroy(sc)
	})
	return done, err
}

// ---------------------------------------------------------------------------
// Report. Ids only: no names, emails or phones.

func (r *authzReport) print(w io.Writer, apply bool) {
	mode := "DRY RUN (nothing written)"
	if apply {
		mode = "APPLY"
	}
	_, _ = fmt.Fprintf(w, "authz-backfill %s\nstaff scanned: %d, firms: %d\n\nCHANGES (narrow or keep, never widen):\n", mode, r.Scanned, r.Firms)
	for _, cat := range changeCategories {
		r.printCategory(w, cat, r.Changes[cat])
	}
	_, _ = fmt.Fprintf(w, "  %-34s %d  (sessions of staff rows already deleted; no firm to audit against)\n", catSessionsRemoved, len(r.RemovedSessions))
	for _, id := range sortedIDs(mapKeys(r.RemovedSessions)) {
		_, _ = fmt.Fprintf(w, "      %s\n", id)
	}
	_, _ = fmt.Fprintf(w, "\nREPORT ONLY (never changed):\n")
	for _, cat := range reportOnlyCategories {
		r.printCategory(w, cat, r.Findings[cat])
	}
	_, _ = fmt.Fprintf(w, "\ntotal changes: %d\n", r.TotalChanges())
	if apply {
		_, _ = fmt.Fprintf(w, "applied: %d", r.Applied)
		if len(r.Conflicts) > 0 {
			_, _ = fmt.Fprintf(w, "; skipped %d changed meanwhile (re-run): %v", len(r.Conflicts), r.Conflicts)
		}
		_, _ = fmt.Fprintln(w)
	}
}

func (r *authzReport) printCategory(w io.Writer, cat string, ids []uuid.UUID) {
	_, _ = fmt.Fprintf(w, "  %-34s %d\n", cat, len(ids))
	for _, id := range sortedIDs(ids) {
		_, _ = fmt.Fprintf(w, "      %s\n", id)
	}
}

func sortedIDs(ids []uuid.UUID) []uuid.UUID {
	out := slices.Clone(ids)
	slices.SortFunc(out, func(a, b uuid.UUID) int { return strings.Compare(a.String(), b.String()) })
	return out
}

func mapKeys(m map[uuid.UUID]int) []uuid.UUID {
	out := make([]uuid.UUID, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
