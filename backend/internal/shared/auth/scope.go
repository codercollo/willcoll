package auth

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"
)

// Property scope (docs/security/authorization.md section 3, D2). A staff
// member restricted to some properties carries that list in the request's
// context; each product's database layer reads it when it opens a tenant
// transaction and hands it to Postgres as app.property_ids, where RESTRICTIVE
// row-level-security policies hide every row outside it. Scope is therefore
// applied by the queries themselves (lists, dashboards, reports, exports and
// search included), not just by middleware in front of them.
//
// No scope in the context means unrestricted: the firm login, owners, staff
// with no property list, and system work (webhooks, jobs), none of which have
// a request principal to scope.

// ErrOutOfScope is what a database layer returns when a write targets a row
// outside the caller's property scope (Postgres refuses it with a
// row-level-security violation). Handlers answer it as 404, the same as a row
// that does not exist, so scope never reveals what exists outside it.
var ErrOutOfScope = errors.New("auth: resource is outside the caller's property scope")

type propertyScopeKey struct{}

// WithPropertyScope returns ctx carrying the property ids the caller is
// restricted to. An empty list returns ctx unchanged (unrestricted).
func WithPropertyScope(ctx context.Context, ids []uuid.UUID) context.Context {
	if len(ids) == 0 {
		return ctx
	}
	return context.WithValue(ctx, propertyScopeKey{}, append([]uuid.UUID(nil), ids...))
}

// PropertyScopeFrom returns the property ids ctx is restricted to, or nil for
// unrestricted.
func PropertyScopeFrom(ctx context.Context) []uuid.UUID {
	ids, _ := ctx.Value(propertyScopeKey{}).([]uuid.UUID)
	return ids
}

// PropertyScopeSetting renders ids as the comma-separated value of the
// app.property_ids setting the row-level-security policies read; "" means
// unrestricted.
func PropertyScopeSetting(ids []uuid.UUID) string {
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = id.String()
	}
	return strings.Join(parts, ",")
}
