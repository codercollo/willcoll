package data

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestSchemaTypingRules enforces the data typing rules documented atop
// models.go against every migration: uuid keys, money as numeric(12,2), and no
// floating-point or auto-increment types anywhere.
func TestSchemaTypingRules(t *testing.T) {
	files, err := filepath.Glob("../../migrations/*.up.sql")
	if err != nil || len(files) == 0 {
		t.Fatalf("no migrations found: %v", err)
	}

	var (
		column      = regexp.MustCompile(`^\s+([a-z_][a-z0-9_]*)\s+([a-z]+(?: precision)?(?:\(\d+(?:,\s*\d+)?\))?)`)
		forbidden   = regexp.MustCompile(`(?i)\b(bigserial|serial|smallserial|real|double precision|float[0-9]*|money)\b`)
		moneyish    = regexp.MustCompile(`(amount|price|fee|balance|deposit|rate|reading|consumed|snapshot)`)
		primaryKey  = regexp.MustCompile(`(?i)^\s+([a-z_]+)\s+(\S+).*\bPRIMARY KEY\b`)
		numericType = regexp.MustCompile(`^numeric`)
	)

	// Deliberate exceptions, each explained in models.go.
	nonUUIDKeys := map[string]bool{"hash": true, "token": true, "key": true}
	notMoney := map[string]bool{"management_fee_percent": true} // a percentage, numeric(5,2)

	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(raw), "\n") {
			code := line
			if c := strings.Index(code, "--"); c >= 0 {
				code = code[:c]
			}
			where := filepath.Base(f) + ":" + itoa(i+1)

			if forbidden.MatchString(code) {
				t.Errorf("%s: forbidden type in %q (no serials, floats or pg money)", where, strings.TrimSpace(code))
			}

			if m := primaryKey.FindStringSubmatch(code); m != nil && !nonUUIDKeys[m[1]] && !strings.EqualFold(m[2], "uuid") {
				t.Errorf("%s: primary key %q is %s, want uuid", where, m[1], m[2])
			}

			m := column.FindStringSubmatch(code)
			if m == nil {
				continue
			}
			name, typ := m[1], strings.ToLower(m[2])
			if !moneyish.MatchString(name) || strings.HasSuffix(name, "_id") || strings.HasSuffix(name, "_at") || name == "period" {
				continue
			}
			switch {
			case notMoney[name]:
				if typ != "numeric(5,2)" {
					t.Errorf("%s: %s is %s, want numeric(5,2)", where, name, typ)
				}
			case numericType.MatchString(typ):
				if typ != "numeric(12,2)" {
					t.Errorf("%s: money column %s is %s, want numeric(12,2)", where, name, typ)
				}
			case typ == "int" || typ == "integer" || typ == "bigint" || typ == "smallint":
				t.Errorf("%s: %s is %s: money is numeric(12,2), never an integer-cents column", where, name, typ)
			}
		}
	}
}

func itoa(n int) string {
	const digits = "0123456789"
	if n == 0 {
		return "0"
	}
	var b []byte
	for ; n > 0; n /= 10 {
		b = append([]byte{digits[n%10]}, b...)
	}
	return string(b)
}
