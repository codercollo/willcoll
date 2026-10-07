package platform

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

// TestPlatformOnlyImportsShared enforces project-structure.txt Part D:
// "internal/platform imports shared only." Every product API depends on
// internal/platform for identity/billing; if it imported a product package
// (internal/rentals today, internal/estates/internal/waste once they
// exist), that product would become an implicit dependency of every other
// product through the platform layer.
func TestPlatformOnlyImportsShared(t *testing.T) {
	const module = "github.com/codercollo/willcoll/backend/"

	forbidden := []string{
		module + "internal/rentals",
		module + "internal/estates",
		module + "internal/waste",
	}

	out, err := exec.Command("go", "list", "-deps", "-json", module+"internal/platform/...").Output()
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
		if !strings.HasPrefix(pkg.ImportPath, module+"internal/platform") {
			continue
		}
		for _, dep := range pkg.Deps {
			for _, bad := range forbidden {
				if dep == bad || strings.HasPrefix(dep, bad+"/") {
					t.Errorf("%s imports %s, a product package — internal/platform may import internal/shared only", pkg.ImportPath, dep)
				}
			}
		}
	}
}
