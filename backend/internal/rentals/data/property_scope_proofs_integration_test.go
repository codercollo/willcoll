package data

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/codercollo/willcoll/backend/internal/rentals/db"
	"github.com/codercollo/willcoll/backend/internal/shared/auth"
	"github.com/codercollo/willcoll/backend/internal/testenv"
	"github.com/google/uuid"
)

// C2 follow-up proofs (docs/security/authorization.md section 3a):
//  1. no scope bleed between requests that share a pooled connection,
//  2. property scope fails closed (migration 000048),
//  3. the tables without a property_scope policy are only ever read through a
//     scoped table.

// 1. One connection, three callers in a row: scoped to A, scoped to B, then
// unrestricted. Each sees exactly its own scope, and the session settings are
// empty again between them (they are transaction-local).
func TestPropertyScopeProof_NoBleedOnASingleConnection(t *testing.T) {
	w := newScopeWorld(t)

	dsn := testenv.Require(t, "WILLCOLL_TEST_DB_DSN")[0]
	one, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { one.Close() })
	one.SetMaxOpenConns(1)
	one.SetMaxIdleConns(1)
	models := NewModelsFromStore(db.NewStore(one), 0)

	settings := func(label string) {
		t.Helper()
		var tenant, scope, ids string
		err := one.QueryRow(`SELECT coalesce(current_setting('app.tenant_id', true), ''),
			coalesce(current_setting('app.property_scope', true), ''),
			coalesce(current_setting('app.property_ids', true), '')`).Scan(&tenant, &scope, &ids)
		if err != nil {
			t.Fatal(err)
		}
		if tenant != "" || scope != "" || ids != "" {
			t.Errorf("after %s the pooled connection still carries tenant=%q scope=%q ids=%q", label, tenant, scope, ids)
		}
	}
	names := func(ctx context.Context) []string {
		t.Helper()
		list, _, err := models.Properties.GetAll(ctx, w.tenant, nil, Filters{Page: 1, PageSize: 50})
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, p := range list {
			out = append(out, p.Name)
		}
		return out
	}

	ctxA := auth.WithPropertyScope(context.Background(), []uuid.UUID{w.propA.ID})
	ctxB := auth.WithPropertyScope(context.Background(), []uuid.UUID{w.propB.ID})
	for round := range 3 {
		if got := names(ctxA); len(got) != 1 || got[0] != "Property A" {
			t.Fatalf("round %d: staff A sees %v, want [Property A]", round, got)
		}
		settings("staff A")
		if got := names(ctxB); len(got) != 1 || got[0] != "Property B" {
			t.Fatalf("round %d: staff B sees %v, want [Property B]", round, got)
		}
		settings("staff B")
		if got := names(context.Background()); len(got) != 2 {
			t.Fatalf("round %d: owner sees %v, want both properties", round, got)
		}
		settings("owner")
	}
	if n := one.Stats().OpenConnections; n != 1 {
		t.Fatalf("pool holds %d connections; the test is only meaningful with exactly 1", n)
	}

	// A failed scoped transaction must not leave its scope behind either.
	_, _ = models.Properties.Get(ctxA, w.tenant, w.propB.ID)
	settings("a not-found scoped read")
	if got := names(context.Background()); len(got) != 2 {
		t.Errorf("owner after a failed scoped read sees %v, want both", got)
	}
}

// 2. Fail closed. Restricted-by-default: without the explicit 'all' marker a
// transaction sees no property data, whatever else is set.
func TestPropertyScopeProof_FailsClosed(t *testing.T) {
	w := newScopeWorld(t)

	// Tables a firm's data lives in, each with rows for the fixture firm.
	tables := []string{"properties", "units", "leases", "ledger_accounts", "charges", "ledger_entries",
		"payments", "payment_allocations", "receipts", "water_readings", "landlords", "reports", "unit_qr_codes", "payment_intents"}

	count := func(settings map[string]string, table string) int {
		t.Helper()
		tx, err := w.conn.Begin()
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		if _, err := tx.Exec(`SELECT set_config('app.tenant_id', $1, true)`, w.tenant.String()); err != nil {
			t.Fatal(err)
		}
		for k, v := range settings {
			if _, err := tx.Exec(`SELECT set_config($1, $2, true)`, k, v); err != nil {
				t.Fatal(err)
			}
		}
		var n int
		if err := tx.QueryRow(fmt.Sprintf(`SELECT count(*) FROM %s`, table)).Scan(&n); err != nil {
			t.Fatalf("%s: %v", table, err)
		}
		return n
	}

	cases := []struct {
		name     string
		settings map[string]string
		wantRows bool
	}{
		{"no scope settings at all", nil, false},
		{"empty marker", map[string]string{"app.property_scope": ""}, false},
		{"restricted with an empty list", map[string]string{"app.property_scope": "list", "app.property_ids": ""}, false},
		{"restricted with the list unset", map[string]string{"app.property_scope": "list"}, false},
		{"marker is case-sensitive", map[string]string{"app.property_scope": "ALL"}, false},
		{"loose truthy value is not 'all'", map[string]string{"app.property_scope": "true"}, false},
		{"explicit all", map[string]string{"app.property_scope": "all"}, true},
		{"explicit all wins over a stale id list", map[string]string{"app.property_scope": "all", "app.property_ids": w.propA.ID.String()}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, table := range tables {
				n := count(tc.settings, table)
				if tc.wantRows && n == 0 {
					t.Errorf("%s: sees no %s rows, want all of them", tc.name, table)
				}
				if !tc.wantRows && n != 0 {
					t.Errorf("%s: sees %d %s rows, want 0", tc.name, n, table)
				}
			}
		})
	}

	t.Run("ids without the marker are still restricted, never unrestricted", func(t *testing.T) {
		got := count(map[string]string{"app.property_ids": w.propA.ID.String()}, "properties")
		if got != 1 {
			t.Errorf("ids of A and no marker sees %d properties, want exactly 1 (A)", got)
		}
	})

	t.Run("restricted list shows only that property", func(t *testing.T) {
		got := count(map[string]string{"app.property_scope": "list", "app.property_ids": w.propA.ID.String()}, "properties")
		if got != 1 {
			t.Errorf("list of A sees %d properties, want 1", got)
		}
	})

	t.Run("a write without the marker is refused", func(t *testing.T) {
		tx, err := w.conn.Begin()
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		if _, err := tx.Exec(`SELECT set_config('app.tenant_id', $1, true)`, w.tenant.String()); err != nil {
			t.Fatal(err)
		}
		_, err = tx.Exec(`INSERT INTO units (tenant_id, property_id, unit_code, status) VALUES ($1, $2, 'NOMARK', 'vacant')`, w.tenant, w.propA.ID)
		if err == nil {
			t.Error("inserted a unit in a transaction with no property_scope marker")
		}
	})

	t.Run("the store always sets the marker", func(t *testing.T) {
		// The API path: no scope in the context means 'all', stated explicitly.
		list, _, err := w.models.Properties.GetAll(context.Background(), w.tenant, nil, Filters{Page: 1, PageSize: 50})
		if err != nil || len(list) != 2 {
			t.Errorf("unrestricted store call sees %d properties (err %v), want 2", len(list), err)
		}
	})
}

// 3. The two tables without a property_scope policy are read only through a
// scoped table. Any new query that touches them must be classified here.
func TestPropertyScopeProof_UnscopedTablesAreOnlyReadThroughScopedOnes(t *testing.T) {
	root := filepath.Join("..", "db", "query")
	files, err := filepath.Glob(filepath.Join(root, "*.sql"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no query files under %s (err %v)", root, err)
	}
	nameRe := regexp.MustCompile(`(?m)^-- name: (\w+)`)

	// query name -> why it is allowed to name the table.
	allowedHeaders := map[string]string{
		"CreateTransactionHeader":      "INSERT only; it reads nothing",
		"ListLedgerEntriesWithBalance": "joined from ledger_entries, which is scoped",
		"OnboardingCounts":             "counted through a join to ledger_entries, which is scoped",
	}
	// pay_otps belongs to the unauthenticated pay surface. The sqlc methods
	// may be called only from the pay-session code, never a manager route.
	otpCallers := map[string]bool{"pay_access.go": true}

	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		src := string(raw)
		idx := nameRe.FindAllStringSubmatchIndex(src, -1)
		for i, m := range idx {
			end := len(src)
			if i+1 < len(idx) {
				end = idx[i+1][0]
			}
			name, body := src[m[2]:m[3]], strings.ToLower(src[m[0]:end])
			// Drop SQL comments so prose does not count as a table reference.
			body = regexp.MustCompile(`(?m)--.*$`).ReplaceAllString(body, "")

			if strings.Contains(body, "transaction_headers") {
				why, ok := allowedHeaders[name]
				if !ok {
					t.Errorf("%s: query %s reads transaction_headers, which has no property_scope policy. Join it from ledger_entries (scoped) and list it in allowedHeaders", filepath.Base(f), name)
					continue
				}
				if !strings.Contains(strings.ToLower(body), "insert into transaction_headers") && !strings.Contains(body, "ledger_entries") {
					t.Errorf("%s: %s is listed as %q but no longer touches ledger_entries", filepath.Base(f), name, why)
				}
			}
			if strings.Contains(body, "pay_otps") && filepath.Base(f) != "pay_otp.sql" {
				t.Errorf("%s: query %s reads pay_otps outside pay_otp.sql", filepath.Base(f), name)
			}
		}
	}

	// The generated pay-OTP methods are called only from the pay-session code.
	otpMethod := regexp.MustCompile(`\.(CountRecentPayOTPs|CreatePayOTP|GetLatestPayOTP|BumpPayOTPAttempts|ConsumePayOTP|PurgePayOTPs)\(`)
	for _, dir := range []string{".", filepath.Join("..", "..", "..", "cmd", "rentals-api")} {
		goFiles, _ := filepath.Glob(filepath.Join(dir, "*.go"))
		for _, f := range goFiles {
			if strings.HasSuffix(f, "_test.go") {
				continue
			}
			raw, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			if otpMethod.Match(raw) && !otpCallers[filepath.Base(f)] {
				t.Errorf("%s calls a pay_otps query; only %v may (the unauthenticated pay surface)", f, otpCallers)
			}
		}
	}
}

// 3b. A caller scoped to property A cannot reach a property B transaction
// through the ledger, the reversal or the receipt paths, nor through the one
// way transaction_headers can be read (a join from ledger_entries).
func TestPropertyScopeProof_NoCrossPropertyTransactionAccess(t *testing.T) {
	w := newScopeWorld(t)
	m := w.models
	owner := context.Background()
	page := Filters{Page: 1, PageSize: 100}

	// Property B's rent ledger, as the owner sees it.
	bLedger, err := m.Ledger.GetUnitLedger(owner, w.tenant, w.unitB.ID, LedgerTypeRent, page)
	if err != nil || len(bLedger.Entries) == 0 {
		t.Fatalf("fixture: owner sees %d entries for B1 (err %v)", len(bLedger.Entries), err)
	}
	bEntries := map[uuid.UUID]uuid.UUID{} // entry id -> header id
	for _, e := range bLedger.Entries {
		bEntries[e.ID] = e.TransactionHeaderID
	}

	t.Run("ledger route", func(t *testing.T) {
		if _, err := m.Ledger.GetUnitLedger(w.scoped, w.tenant, w.unitB.ID, LedgerTypeRent, page); !errors.Is(err, ErrRecordNotFound) {
			t.Errorf("ledger of B1 as A = %v, want not found", err)
		}
		aLedger, err := m.Ledger.GetUnitLedger(w.scoped, w.tenant, w.unitA.ID, LedgerTypeRent, page)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range aLedger.Entries {
			if _, leaked := bEntries[e.ID]; leaked {
				t.Errorf("A's ledger contains B's entry %s", e.ID)
			}
			for _, h := range bEntries {
				if h == e.TransactionHeaderID {
					t.Errorf("A's ledger shows B's transaction %s", h)
				}
			}
		}
	})

	t.Run("reversal route", func(t *testing.T) {
		before := countHeaders(t, w, "REVERSAL")
		for entryID := range bEntries {
			_, err := m.Rent.ReverseEntry(w.scoped, w.tenant, w.tenant, entryID, "cross-property attempt")
			if !errors.Is(err, ErrRecordNotFound) {
				t.Errorf("reversing B's entry %s as A = %v, want not found", entryID, err)
			}
		}
		if after := countHeaders(t, w, "REVERSAL"); after != before {
			t.Errorf("reversal headers went from %d to %d: a refused reversal still wrote", before, after)
		}
	})

	t.Run("receipt route", func(t *testing.T) {
		// Receipts are filed under the period they were issued in (today's),
		// so read it from the fixture rather than guessing.
		var firstDay time.Time
		biTx(t, w.conn, w.tenant, func(tx *sql.Tx) {
			if err := tx.QueryRow(`SELECT min(period) FROM receipts`).Scan(&firstDay); err != nil {
				t.Fatal(err)
			}
		})
		period := biPeriod(t, firstDay.Format("2006-01"))
		if rs, err := m.Receipts.Generate(owner, w.tenant, w.propB.ID, period, nil); err != nil || len(rs) == 0 {
			t.Fatalf("fixture: owner sees %d receipts for B (err %v)", len(rs), err)
		}
		none := func(err error) bool { return errors.Is(err, ErrRecordNotFound) || errors.Is(err, ErrNothingToGenerate) }
		if rs, err := m.Receipts.Generate(w.scoped, w.tenant, w.propB.ID, period, nil); len(rs) != 0 || (err != nil && !none(err)) {
			t.Errorf("receipts of B as A = %d (err %v), want none or not found", len(rs), err)
		}
		if rs, err := m.Receipts.Generate(w.scoped, w.tenant, w.propB.ID, period, &w.unitB.ID); len(rs) != 0 || (err != nil && !none(err)) {
			t.Errorf("receipts of B1 as A = %d (err %v), want none or not found", len(rs), err)
		}
		if rs, err := m.Receipts.Generate(w.scoped, w.tenant, w.propA.ID, period, &w.unitB.ID); len(rs) != 0 || (err != nil && !none(err)) {
			t.Errorf("receipts of B1 asked through property A = %d (err %v), want none or not found", len(rs), err)
		}
		if rs, err := m.Receipts.Generate(w.scoped, w.tenant, w.propA.ID, period, nil); err != nil || len(rs) == 0 {
			t.Errorf("receipts of A as A = %d (err %v), want some", len(rs), err)
		}
	})

	t.Run("transaction_headers only through ledger_entries", func(t *testing.T) {
		// The only way a header is read: joined from the scoped ledger_entries.
		// A caller scoped to A must see exactly the headers of A's entries.
		tx, err := w.conn.Begin()
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		if _, err := tx.Exec(`SELECT set_config('app.tenant_id', $1, true), set_config('app.property_scope', 'list', true), set_config('app.property_ids', $2, true)`,
			w.tenant.String(), w.propA.ID.String()); err != nil {
			t.Fatal(err)
		}
		rows, err := tx.Query(`SELECT DISTINCT h.id FROM transaction_headers h JOIN ledger_entries le ON le.transaction_header_id = h.id`)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		seen := 0
		bHeaders := map[uuid.UUID]bool{}
		for _, h := range bEntries {
			bHeaders[h] = true
		}
		for rows.Next() {
			var id uuid.UUID
			if err := rows.Scan(&id); err != nil {
				t.Fatal(err)
			}
			seen++
			if bHeaders[id] {
				t.Errorf("joined read from ledger_entries returned B's header %s", id)
			}
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		if seen == 0 {
			t.Error("scoped join returned no headers; the check proves nothing")
		}
	})
}

// countHeaders counts transaction headers of one type for the fixture firm,
// read as an unrestricted caller.
func countHeaders(t *testing.T, w *scopeWorld, typ string) int {
	t.Helper()
	var n int
	biTx(t, w.conn, w.tenant, func(tx *sql.Tx) {
		if err := tx.QueryRow(`SELECT count(*) FROM transaction_headers WHERE type = $1`, typ).Scan(&n); err != nil {
			t.Fatal(err)
		}
	})
	return n
}
