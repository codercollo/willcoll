package data

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/codercollo/willcoll/backend/internal/db/sqlc"
)

// appendOnlyTables never receive an UPDATE or DELETE (ADR 0002). The
// database enforces it with revoked grants (migrations 000018/000019); this
// test enforces it in code, so the mistake fails in CI rather than as a
// permission error in production. (Greenlight ch.8.2 divergence: these
// rows have no version column and no Update method, because a posted
// ledger row is reversed with a storno, never edited.)
var appendOnlyTables = []string{"charges", "transaction_headers", "ledger_entries", "payment_allocations"}

func TestAppendOnlyQueries(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("..", "db", "query", "*.sql"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no query files found: %v", err)
	}

	for _, table := range appendOnlyTables {
		mutation := regexp.MustCompile(`(?i)\b(UPDATE\s+` + table + `|DELETE\s+FROM\s+` + table + `)\b`)
		for _, f := range files {
			src, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			for _, line := range strings.Split(string(src), "\n") {
				if strings.HasPrefix(strings.TrimSpace(line), "--") {
					continue // comments may mention the rule
				}
				if mutation.MatchString(line) {
					t.Errorf("%s mutates append-only table %s: %q", filepath.Base(f), table, strings.TrimSpace(line))
				}
			}
		}
	}
}

func TestAppendOnlyQuerierMethods(t *testing.T) {
	forbidden := regexp.MustCompile(`^(Update|Delete|Archive|Upsert)\w*(Charge|TransactionHeader|LedgerEntr|PaymentAllocation)`)

	querier := reflect.TypeOf((*sqlc.Querier)(nil)).Elem()
	for i := 0; i < querier.NumMethod(); i++ {
		if name := querier.Method(i).Name; forbidden.MatchString(name) {
			t.Errorf("sqlc.Querier has %s, a mutation of an append-only table", name)
		}
	}
}

func TestAppendOnlyModels(t *testing.T) {
	for _, model := range []any{LedgerModel{}, PaymentModel{}} {
		typ := reflect.TypeOf(model)
		for i := 0; i < typ.NumMethod(); i++ {
			name := typ.Method(i).Name
			if strings.HasPrefix(name, "Update") || strings.HasPrefix(name, "Delete") {
				t.Errorf("%s.%s: append-only models must not update or delete", typ.Name(), name)
			}
		}
	}
}
