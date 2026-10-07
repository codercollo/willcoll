// Package testenv is how integration tests find their databases.
//
// Run directly (`go test ./...`) a test whose database DSN is unset skips,
// so the plain unit run needs no Postgres. Run through scripts/
// test_integration.sh (make test/integration), which sets INTEGRATION=1 and
// every DSN, a missing DSN FAILS the test instead: an integration run where
// tests quietly skipped is a green build that checked nothing.
package testenv

import (
	"os"
	"testing"
)

// Require returns the values of the named environment variables, in order.
// If any is empty it skips the test, or fails it when INTEGRATION=1.
func Require(t testing.TB, names ...string) []string {
	t.Helper()
	out := make([]string, len(names))
	var missing []string
	for i, n := range names {
		out[i] = os.Getenv(n)
		if out[i] == "" {
			missing = append(missing, n)
		}
	}
	if len(missing) == 0 {
		return out
	}
	if os.Getenv("INTEGRATION") == "1" {
		t.Fatalf("INTEGRATION=1 but %v not set: the integration run must provide every test database (scripts/test_integration.sh does)", missing)
	}
	t.Skipf("%v not set; skipping (set INTEGRATION=1 via make test/integration to require it)", missing)
	return out
}
