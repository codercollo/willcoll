package shared

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

// TestSharedDoesNotImportRentals enforces the same rule as .golangci.yml's
// depguard config, independently of golangci-lint: internal/shared/* is
// imported by every product's API binary, so it must never depend on a
// product package (internal/rentals, and internal/estates/internal/waste
// once they exist) — otherwise that product becomes an implicit dependency
// of every other product (system-design.txt Part A.5, project-structure.txt
// Part D step 1).
func TestSharedDoesNotImportRentals(t *testing.T) {
	const module = "github.com/codercollo/willcoll/backend/"

	forbidden := []string{
		module + "internal/rentals",
		module + "internal/estates",
		module + "internal/waste",
	}

	out, err := exec.Command("go", "list", "-deps", "-json", module+"internal/shared/...").Output()
	if err != nil {
		t.Fatalf("go list -deps: %v", err)
	}

	dec := json.NewDecoder(strings.NewReader(string(out)))
	for dec.More() {
		var pkg struct {
			ImportPath string
			Deps       []string
		}
		if err := dec.Decode(&pkg); err != nil {
			t.Fatalf("decoding go list output: %v", err)
		}
		if !strings.HasPrefix(pkg.ImportPath, module+"internal/shared") {
			continue
		}
		for _, dep := range pkg.Deps {
			for _, bad := range forbidden {
				if dep == bad || strings.HasPrefix(dep, bad+"/") {
					t.Errorf("%s imports %s, a product package — internal/shared must stay product-agnostic", pkg.ImportPath, dep)
				}
			}
		}
	}
}
