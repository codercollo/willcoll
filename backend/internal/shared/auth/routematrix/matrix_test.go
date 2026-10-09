package routematrix

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/codercollo/willcoll/backend/internal/shared/auth"
)

func TestFormatParseRoundTrip(t *testing.T) {
	all := map[string]Matrix{
		"a-api": {"GET /v1/x": {"anonymous": "401", "viewer": "200", "officer": "step-up>200*"}},
		"b-api": {"POST /v1/y/:id": {"anonymous": "401", "firm_login": "422"}},
	}
	got, err := Parse(Format(all))
	if err != nil {
		t.Fatal(err)
	}
	for api, m := range all {
		for route, cells := range m {
			for _, p := range Principals {
				want := cells[p]
				if want == "" {
					want = "-"
				}
				if got[api][route][p] != want {
					t.Errorf("%s %s %s = %q, want %q", api, route, p, got[api][route][p], want)
				}
			}
		}
	}
}

func TestDiffReportsEveryKindOfChange(t *testing.T) {
	want := Matrix{"GET /a": {"anonymous": "401"}, "GET /gone": {"anonymous": "401"}}
	got := Matrix{"GET /a": {"anonymous": "200"}, "GET /new": {"anonymous": "401"}}
	diff := strings.Join(Diff(want, got), "\n")
	for _, s := range []string{"GET /a as anonymous: golden 401, now 200", "GET /gone", "GET /new"} {
		if !strings.Contains(diff, s) {
			t.Errorf("diff does not mention %q:\n%s", s, diff)
		}
	}
	if len(Diff(want, want)) != 0 {
		t.Error("identical matrices differ")
	}
}

func TestCell(t *testing.T) {
	stepUp := `{"error":{"code":"step_up_required"}}`
	for _, c := range []struct {
		status  int
		body    string
		reached bool
		want    string
	}{
		{403, stepUp, false, "step-up"},
		{403, stepUp, true, "step-up"}, // asked inside the handler: still a step-up demand
		{403, "nope", false, "403"},
		{403, "nope", true, "403"}, // a handler-level permission refusal is a 403, not 403h
		{401, "", false, "401"},
		{201, "", true, "201"},
		{404, "", false, "404"}, // refused by scope: the handler never ran
		{404, "", true, "404h"}, // the handler ran and found nothing
		{422, "", true, "422h"},
		{409, "", true, "409h"},
		{503, "", true, "503h"},
	} {
		if got := Cell(c.status, c.body, c.reached); got != c.want {
			t.Errorf("Cell(%d, reached=%v) = %q, want %q", c.status, c.reached, got, c.want)
		}
	}
}

func TestStepUpCellsAndVariantKeys(t *testing.T) {
	cell := WithStepUp(StepUpCell, "200")
	if cell != "step-up>200*" || !AskedForStepUp(cell) || !IsSuccess(cell) || Final(cell) != "200" {
		t.Errorf("a step-up cell that succeeded afterwards: %q", cell)
	}
	if again := WithStepUp(StepUpCell, "422h"); IsSuccess(again) || Refusal(again) != "422" {
		t.Errorf("a step-up cell that was rejected afterwards: %q", again)
	}
	if IsSuccess(StepUpCell) || IsSuccess("403") || !IsSuccess("201") || IsSuccess("422h") {
		t.Error("IsSuccess misclassifies")
	}
	key := VariantKey("PATCH /v1/staff/:id", "role change")
	if key != "PATCH /v1/staff/:id [role change]" || BaseKey(key) != "PATCH /v1/staff/:id" || !IsVariant(key) || IsVariant(BaseKey(key)) {
		t.Errorf("variant key %q", key)
	}
	if got := mdCell(cell); got != "step-up → 2xx*" {
		t.Errorf("mdCell(%q) = %q", cell, got)
	}
	if got := mdCell(WithStepUp(StepUpCell, "422h")); got != "step-up → 422h*" {
		t.Errorf("mdCell = %q", got)
	}

	// A variant row survives the golden file's text form.
	all := map[string]Matrix{"a-api": {key: {"anonymous": "401", "staff_owner": cell}}}
	got, err := Parse(Format(all))
	if err != nil {
		t.Fatal(err)
	}
	if got["a-api"][key]["staff_owner"] != cell {
		t.Errorf("round trip lost the variant row: %v", got["a-api"])
	}
}

// requireDocs skips a test when docs/ is not beside backend/: `make test/race`
// copies only backend/ into its container. Everywhere the whole repository is
// present (a plain go test, make test/integration, CI) the test runs.
func requireDocs(t *testing.T) {
	t.Helper()
	if _, err := os.Stat(filepath.Dir(DocPath())); err != nil {
		t.Skip("docs/security is not available here (backend-only checkout); skipping")
	}
}

// The markdown is the golden file, rendered. Edited by hand or left behind
// after a regeneration, it fails here.
func TestMarkdownIsInSyncWithTheGoldenFile(t *testing.T) {
	requireDocs(t)
	all, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) == 0 {
		t.Skip("no golden file yet")
	}
	doc, err := os.ReadFile(DocPath())
	if err != nil {
		t.Fatalf("%v: generate it with -update", err)
	}
	if string(doc) != Markdown(all) {
		t.Errorf("%s is out of date with the golden file; regenerate with -update", DocPath())
	}
}

// Every API's golden section covers exactly the principals above.
func TestGoldenRowsAreComplete(t *testing.T) {
	all, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	for api, m := range all {
		for route, cells := range m {
			for _, p := range Principals {
				if cells[p] == "" || cells[p] == "-" {
					t.Errorf("%s %s has no result for %s", api, route, p)
				}
			}
		}
	}
}

// Every permission in the catalog is described in authorization.md.
func TestEveryCatalogPermissionIsDocumented(t *testing.T) {
	requireDocs(t)
	authDoc, err := os.ReadFile(strings.Replace(DocPath(), "route-matrix.md", "authorization.md", 1))
	if err != nil {
		t.Fatal(err)
	}
	for _, perm := range auth.Catalog() {
		if !strings.Contains(string(authDoc), perm) {
			t.Errorf("permission %q is in the catalog but not in docs/security/authorization.md", perm)
		}
	}
}
