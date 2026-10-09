package auth

import (
	"fmt"
	"path"
	"sort"
	"sync"

	"github.com/gin-gonic/gin"
)

// RouteTable records, for every route an API registers, the permission that
// guards it or the reason it is deliberately public (docs/security/
// authorization.md, deny by default). Each API registers routes ONLY through
// Guard / Public, and a test compares the table with the router's own route
// list, so a route added any other way (or with an unknown permission, or as
// public without a reviewed allowlist entry) fails the build.
type RouteTable struct {
	mu      sync.Mutex
	entries map[string]RouteGuard
}

// RouteGuard is one route's entry: exactly one of Permission or PublicReason
// is set.
type RouteGuard struct {
	Permission   string
	PublicReason string
}

func NewRouteTable() *RouteTable { return &RouteTable{entries: map[string]RouteGuard{}} }

// Key is the "METHOD /full/path" form used by the table, the public
// allowlist and router.Routes().
func Key(method, fullPath string) string { return method + " " + fullPath }

func (t *RouteTable) record(key string, g RouteGuard) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, dup := t.entries[key]; dup {
		panic(fmt.Sprintf("auth: route %s registered twice", key))
	}
	t.entries[key] = g
}

// Lookup returns the entry for one route key.
func (t *RouteTable) Lookup(key string) (RouteGuard, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	g, ok := t.entries[key]
	return g, ok
}

// Entries returns a copy of the table.
func (t *RouteTable) Entries() map[string]RouteGuard {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make(map[string]RouteGuard, len(t.entries))
	for k, v := range t.entries {
		out[k] = v
	}
	return out
}

// Keys returns the recorded route keys, sorted.
func (t *RouteTable) Keys() []string {
	e := t.Entries()
	keys := make([]string, 0, len(e))
	for k := range e {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// Guard registers a route that requires perm. require builds the
// per-app middleware enforcing it (it runs before the handlers). An unknown
// permission panics at startup.
func Guard(t *RouteTable, rg *gin.RouterGroup, method, p, perm string, require func(string) gin.HandlerFunc, handlers ...gin.HandlerFunc) {
	if !Known(perm) {
		panic(fmt.Sprintf("auth: route %s %s uses unknown permission %q", method, p, perm))
	}
	t.record(Key(method, path.Join(rg.BasePath(), p)), RouteGuard{Permission: perm})
	rg.Handle(method, p, append([]gin.HandlerFunc{require(perm)}, handlers...)...)
}

// Public registers a route with no permission. It must appear in allow (the
// app's reviewed public allowlist, keyed by Key, valued by the reason it is
// public and what protects it), otherwise startup panics. handlers carry the
// route's own protection (rate limit, token or signature check, ...).
func Public(t *RouteTable, rg *gin.RouterGroup, allow map[string]string, method, p string, handlers ...gin.HandlerFunc) {
	key := Key(method, path.Join(rg.BasePath(), p))
	reason, ok := allow[key]
	if !ok {
		panic(fmt.Sprintf("auth: public route %s is not in the reviewed public allowlist", key))
	}
	t.record(key, RouteGuard{PublicReason: reason})
	rg.Handle(method, p, handlers...)
}

// Problems compares the router's registered routes (Key form) with the table
// and the public allowlist and returns every violation of deny by default:
// a route registered without Guard/Public, a table entry with no route, an
// unknown permission, or an allowlist entry that no route uses (a stale
// public entry would silently re-open a removed route).
func (t *RouteTable) Problems(registered []string, allow map[string]string) []string {
	entries := t.Entries()
	reg := map[string]bool{}
	var out []string
	for _, k := range registered {
		reg[k] = true
		if _, ok := entries[k]; !ok {
			out = append(out, k+": registered without a permission or public allowlist entry")
		}
	}
	for k, g := range entries {
		switch {
		case !reg[k]:
			out = append(out, k+": in the route table but not registered")
		case g.Permission == "" && g.PublicReason == "":
			out = append(out, k+": neither a permission nor a public reason")
		case g.Permission != "" && g.PublicReason != "":
			out = append(out, k+": both a permission and a public reason")
		case g.Permission != "" && !Known(g.Permission):
			out = append(out, k+": unknown permission "+g.Permission)
		}
	}
	for k := range allow {
		if g, ok := entries[k]; !ok || g.PublicReason == "" {
			out = append(out, k+": in the public allowlist but not registered as public")
		}
	}
	sort.Strings(out)
	return out
}
