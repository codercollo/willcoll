// Package routematrix is the golden route x role matrix (docs/security/
// c4-spec.md C4.2): for every route an API registers, the status each kind of
// caller gets, stored in one reviewed file and compared on every test run.
//
// Each API's test (cmd/platform-api, cmd/rentals-api) calls its routes as every
// principal in Principals against a real database and hands the observed
// statuses to Check. The golden file holds one section per API; docs/security/
// route-matrix.md is generated from it. A change in who can call what is a
// diff in the golden file, and a diff has to be read and accepted:
//
//	go test ./cmd/platform-api ./cmd/rentals-api -run RouteMatrix -update
//
// regenerates the file and the markdown. Never regenerate without reading the
// diff.
package routematrix

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/codercollo/willcoll/backend/internal/shared/auth"
)

// Principals are the callers every route is exercised as, in column order.
var Principals = []string{
	"anonymous",      // no credentials
	"pay_token",      // a valid tenant pay token for unit A1 (platform-api has no pay surface: a bearer token it does not know)
	"viewer",         // staff, role viewer
	"officer",        // staff, role officer, unscoped
	"officer_scoped", // staff, role officer, property A only (the routes ask for property B's resources)
	"accountant",     // staff, role accountant
	"staff_owner",    // staff, role owner
	"firm_login",     // the firm's own login (the implicit owner)
	"super_admin",    // the Super Admin
	"service",        // the service-to-service credential
}

// StepUpCell is the cell of a refused request that asks the caller to re-enter
// their password (403 step_up_required). When the same request is repeated with a
// fresh step-up window the cell grows the outcome: "step-up>200*", where the
// star means "after step-up".
const StepUpCell = "step-up"

// afterStepUp separates the two halves of a step-up cell.
const afterStepUp = ">"

// ReachedHeader is the response header the handler-reached marker sets, in tests
// only (auth.MarkReached): its presence means authorization let the request
// through to the route's own handler. The golden file records it as an "h" on the
// status (404h, 422h, ...); without it a 404 is a refusal by scope.
const ReachedHeader = auth.ReachedHeader

// Matrix is one API's result: row key -> principal -> cell. A row key is
// "METHOD /full/path", or "METHOD /full/path [variant]" for a realistic request
// to a route whose rule depends on what is being changed (see Variant).
type Matrix map[string]map[string]string

var update = flag.Bool("update", false, "regenerate docs/security/route-matrix.md and the golden route matrix (read the diff!)")

// VariantKey is the row key of a named request variant of a route.
func VariantKey(route, variant string) string { return route + " [" + variant + "]" }

// BaseKey strips a row key's " [variant]" suffix: the registered route it is a
// row of.
func BaseKey(key string) string {
	if base, rest, ok := strings.Cut(key, " ["); ok && strings.HasSuffix(rest, "]") {
		return base
	}
	return key
}

// IsVariant reports whether a row key is a named variant row.
func IsVariant(key string) bool { return BaseKey(key) != key }

// Cell renders one observed response.
//
//	"401", "403"        refused by authentication or by permission
//	"403" + step-up     "step-up": refused until the password is re-entered
//	"404"               refused by scope (the handler never ran)
//	"404h", "422h", ... the handler ran (authorization let the request through)
//	                    and answered with that code
//	"200", "201", ...   success (always from a handler that ran)
//
// reached is whether the handler-reached marker was set on the response.
func Cell(status int, body string, reached bool) string {
	if status == 403 && strings.Contains(body, "step_up_required") {
		return StepUpCell
	}
	cell := strconv.Itoa(status)
	if reached && status/100 != 2 && status != 401 && status != 403 {
		cell += "h"
	}
	return cell
}

// WithStepUp joins a refused-until-step-up cell with the outcome of repeating
// the request inside an open window: "step-up>200*".
func WithStepUp(first, afterwards string) string {
	return first + afterStepUp + afterwards + "*"
}

// AskedForStepUp reports whether a cell is, or starts as, a step-up demand.
func AskedForStepUp(cell string) bool { return strings.HasPrefix(cell, StepUpCell) }

// Final is a cell's outcome: for a step-up cell, what happened once the password
// was re-entered ("200" from "step-up>200*"); otherwise the cell itself.
func Final(cell string) string {
	if _, after, ok := strings.Cut(cell, afterStepUp); ok {
		return strings.TrimSuffix(after, "*")
	}
	return cell
}

// IsSuccess reports whether a cell is a 2xx (after step-up, where it asked).
func IsSuccess(cell string) bool { return strings.HasPrefix(Final(cell), "2") }

// Refusal is the status code without the handler-reached mark.
func Refusal(cell string) string { return strings.TrimSuffix(Final(cell), "h") }

// Section is the part of the golden file for one API.
const (
	APIPlatform = "platform-api"
	APIRentals  = "rentals-api"
)

func here() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Dir(file)
}

// GoldenPath is backend/internal/shared/auth/testdata/route_matrix.golden.
func GoldenPath() string {
	return filepath.Join(here(), "..", "testdata", "route_matrix.golden")
}

// DocPath is docs/security/route-matrix.md.
func DocPath() string {
	return filepath.Join(here(), "..", "..", "..", "..", "..", "docs", "security", "route-matrix.md")
}

// Format renders sections (API name -> matrix) as the golden file.
func Format(all map[string]Matrix) string {
	var b strings.Builder
	b.WriteString("# Golden route x role matrix (docs/security/c4-spec.md C4.2). Generated: do not edit by hand.\n")
	b.WriteString("# Regenerate with: go test ./cmd/platform-api ./cmd/rentals-api -run RouteMatrix -update   (then read the diff)\n")
	fmt.Fprintf(&b, "# columns: %s\n", strings.Join(Principals, " "))
	apis := make([]string, 0, len(all))
	for api := range all {
		apis = append(apis, api)
	}
	sort.Strings(apis)
	for _, api := range apis {
		fmt.Fprintf(&b, "\n== %s\n", api)
		m := all[api]
		routes := make([]string, 0, len(m))
		for r := range m {
			routes = append(routes, r)
		}
		sort.Strings(routes)
		for _, r := range routes {
			cells := make([]string, len(Principals))
			for i, p := range Principals {
				cells[i] = m[r][p]
				if cells[i] == "" {
					cells[i] = "-"
				}
			}
			fmt.Fprintf(&b, "%s | %s\n", r, strings.Join(cells, " "))
		}
	}
	return b.String()
}

// Parse reads the golden file format.
func Parse(text string) (map[string]Matrix, error) {
	all := map[string]Matrix{}
	var cur Matrix
	sc := bufio.NewScanner(strings.NewReader(text))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		switch {
		case line == "" || strings.HasPrefix(line, "#"):
		case strings.HasPrefix(line, "== "):
			cur = Matrix{}
			all[strings.TrimPrefix(line, "== ")] = cur
		default:
			if cur == nil {
				return nil, fmt.Errorf("routematrix: row before any section: %q", line)
			}
			route, cells, ok := strings.Cut(line, " | ")
			fields := strings.Fields(cells)
			if !ok || len(fields) != len(Principals) {
				return nil, fmt.Errorf("routematrix: bad row %q: want %d cells", line, len(Principals))
			}
			row := map[string]string{}
			for i, p := range Principals {
				row[p] = fields[i]
			}
			cur[route] = row
		}
	}
	return all, sc.Err()
}

// Load reads the golden file (an empty set if it does not exist yet).
func Load() (map[string]Matrix, error) {
	raw, err := os.ReadFile(GoldenPath())
	if os.IsNotExist(err) {
		return map[string]Matrix{}, nil
	}
	if err != nil {
		return nil, err
	}
	return Parse(string(raw))
}

// Check compares got with the API's section of the golden file and fails the
// test on any difference. With -update it rewrites the section and the
// markdown instead.
func Check(t testing.TB, api string, got Matrix) {
	t.Helper()
	all, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if *update {
		all[api] = got
		if err := os.MkdirAll(filepath.Dir(GoldenPath()), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(GoldenPath(), []byte(Format(all)), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(DocPath(), []byte(Markdown(all)), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("wrote %s and %s: read the diff before committing", GoldenPath(), DocPath())
		return
	}
	want, ok := all[api]
	if !ok {
		t.Fatalf("no %s section in %s; generate it with -update and read it", api, GoldenPath())
	}
	for _, d := range Diff(want, got) {
		t.Error(d)
	}
}

// Diff lists every difference between a golden section and an observed one.
func Diff(want, got Matrix) []string {
	var out []string
	for route := range want {
		if _, ok := got[route]; !ok {
			out = append(out, route+": in the golden file but no longer a registered route")
		}
	}
	for route, cells := range got {
		w, ok := want[route]
		if !ok {
			out = append(out, route+": a new route that is not in the golden file")
			continue
		}
		for _, p := range Principals {
			if w[p] != cells[p] {
				out = append(out, fmt.Sprintf("%s as %s: golden %s, now %s", route, p, w[p], cells[p]))
			}
		}
	}
	sort.Strings(out)
	return out
}

// ---------------------------------------------------------------------------
// Markdown.

// Resource is the group a route is listed under: its first path segment after
// /v1 ("admin/managers" for the admin routes, "internal" for /internal/v1).
func Resource(route string) string {
	_, p, _ := strings.Cut(BaseKey(route), " ")
	p = strings.TrimPrefix(p, "/")
	segs := strings.Split(p, "/")
	switch {
	case len(segs) >= 3 && segs[0] == "v1" && segs[1] == "admin":
		return "admin/" + segs[2]
	case len(segs) >= 2 && segs[0] == "v1":
		return segs[1]
	case len(segs) >= 2 && segs[0] == "internal":
		return "internal"
	case len(segs) >= 1:
		return segs[0]
	}
	return "other"
}

func mdCell(cell string) string {
	switch {
	case AskedForStepUp(cell) && strings.Contains(cell, afterStepUp):
		after := Final(cell)
		if strings.HasPrefix(after, "2") {
			after = "2xx"
		}
		return "step-up → " + after + "*"
	case IsSuccess(cell):
		return "2xx"
	}
	return cell
}

// Markdown renders the human-readable copy: one table per API, routes grouped
// by resource.
func Markdown(all map[string]Matrix) string {
	var b strings.Builder
	b.WriteString("# Route x role matrix\n\n")
	b.WriteString("Generated from `backend/internal/shared/auth/testdata/route_matrix.golden` by the route-matrix tests " +
		"(`go test ./cmd/platform-api ./cmd/rentals-api -run RouteMatrix -update`). Do not edit by hand.\n\n")
	b.WriteString("Every route is called as every principal against a real database with a seeded fixture " +
		"(`docs/security/c4-spec.md` C4.2, `docs/security/c4-matrix-fixes.md`). A row `METHOD /path [variant]` " +
		"is the same route called with a specific, realistic body, for routes whose rule depends on what is being changed.\n\n" +
		"| Cell | Meaning |\n|---|---|\n" +
		"| `2xx` | the request reached the handler and succeeded |\n" +
		"| `404h` `422h` `409h` `400h` `503h` ... | **the handler ran** (authorization let the request through) and returned that code |\n" +
		"| `404` | refused by scope: out of the caller's properties or firm; the handler never ran |\n" +
		"| `403` | refused by permission |\n" +
		"| `401` | not authenticated |\n" +
		"| `step-up` | refused until the caller re-enters their password |\n" +
		"| `step-up → 2xx*` | refused until step-up; `*` = the same request, repeated with a fresh step-up window, then gave that outcome |\n" +
		"| `-` | not exercised |\n\n" +
		"`officer_scoped` is restricted to property A and asks for property B's resources. " +
		"`pay_token` is a valid tenant pay token for unit A1.\n")
	apis := make([]string, 0, len(all))
	for api := range all {
		apis = append(apis, api)
	}
	sort.Strings(apis)
	for _, api := range apis {
		m := all[api]
		fmt.Fprintf(&b, "\n## %s\n\n| Route | %s |\n|---|%s\n", api, strings.Join(Principals, " | "), strings.Repeat("---|", len(Principals)))
		routes := make([]string, 0, len(m))
		for r := range m {
			routes = append(routes, r)
		}
		sort.Slice(routes, func(i, j int) bool {
			ri, rj := Resource(routes[i]), Resource(routes[j])
			if ri != rj {
				return ri < rj
			}
			return routes[i] < routes[j]
		})
		last := ""
		for _, r := range routes {
			if res := Resource(r); res != last {
				fmt.Fprintf(&b, "| **%s** |%s\n", res, strings.Repeat(" |", len(Principals)))
				last = res
			}
			cells := make([]string, len(Principals))
			for i, p := range Principals {
				cells[i] = mdCell(m[r][p])
			}
			fmt.Fprintf(&b, "| `%s` | %s |\n", r, strings.Join(cells, " | "))
		}
	}
	return b.String()
}
