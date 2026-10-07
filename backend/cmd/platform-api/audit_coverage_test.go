package main

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// auditCoverage classifies every write route (build-phases.txt 3f-4) as
// "audited" (its handler must call app.audit/app.auditAdmin) or
// "exempt: <reason>". A route missing from the map fails TestAuditCoverage.
// See cmd/rentals-api/audit_coverage_test.go for the identical pattern.
var auditCoverage = map[string]string{
	"POST /v1/managers":              "exempt: signup, no tenant session yet",
	"PUT /v1/managers/activated":     "exempt: identity lifecycle, pre-session",
	"POST /v1/tokens/activation":     "exempt: resend email, no tenant mutation",
	"PUT /v1/staff/activated":        "exempt: identity lifecycle, pre-session",
	"POST /v1/sessions":              "exempt: login, not a tenant mutation",
	"DELETE /v1/sessions":            "exempt: logout",
	"POST /v1/admin/sessions":        "exempt: admin login",
	"POST /v1/tokens/password-reset": "exempt: resend email",
	"PUT /v1/managers/password":      "exempt: password reset via emailed token, pre-session",

	"PATCH /v1/account":         "audited",
	"PUT /v1/account/password":  "audited",
	"PUT /v1/staff/me/password": "audited",

	"POST /v1/staff":      "audited",
	"PATCH /v1/staff/:id": "audited",

	"POST /v1/billing/:product/subscription/renew": "audited",
	"POST /v1/modules/:product/trial":              "audited",

	"POST /v1/webhooks/payhero/subscriptions": "exempt: webhook, logged elsewhere (processSubscription)",

	"POST /v1/admin/managers/:id/suspend":   "audited",
	"POST /v1/admin/managers/:id/reinstate": "audited",

	"POST /v1/admin/plans":             "exempt: global plan catalog, no manager tenant to scope an audit row to",
	"PATCH /v1/admin/plans/:id":        "exempt: global plan catalog, no manager tenant to scope an audit row to",
	"POST /v1/admin/plans/:id/archive": "exempt: global plan catalog, no manager tenant to scope an audit row to",
	"POST /v1/admin/plans/:id/restore": "exempt: global plan catalog, no manager tenant to scope an audit row to",
}

func isWriteMethod(m string) bool {
	switch m {
	case "POST", "PUT", "PATCH", "DELETE":
		return true
	}
	return false
}

// handlerCallsAudit reports whether handler's source calls app.audit( or
// app.auditAdmin( anywhere in its body. See cmd/rentals-api's identical
// helper for why this greps source instead of using reflect/runtime
// directly (an unexported bound method's PC has no real source location).
func handlerCallsAudit(t *testing.T, handler gin.HandlerFunc) bool {
	t.Helper()
	wrapperName := runtime.FuncForPC(reflect.ValueOf(handler).Pointer()).Name()
	methodName := strings.TrimSuffix(wrapperName[strings.LastIndex(wrapperName, ".")+1:], "-fm")

	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("listing source files: %v", err)
	}
	decl := "func (app *application) " + methodName + "("
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		b, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("reading %s: %v", file, err)
		}
		idx := strings.Index(string(b), decl)
		if idx < 0 {
			continue
		}
		lines := strings.Split(string(b[idx:]), "\n")
		for i, line := range lines {
			if i > 0 && strings.HasPrefix(line, "func ") {
				break
			}
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "//") {
				continue
			}
			if strings.Contains(line, "app.audit(") || strings.Contains(line, "app.auditAdmin(") {
				return true
			}
		}
		return false
	}
	t.Fatalf("could not find declaration %q in any source file", decl)
	return false
}

func TestAuditCoverage(t *testing.T) {
	app := &application{}
	app.config.cors.trustedOrigins = []string{"http://localhost:3000"}
	router, err := app.routes()
	if err != nil {
		t.Fatal(err)
	}

	for _, r := range router.Routes() {
		if !isWriteMethod(r.Method) {
			continue
		}
		key := r.Method + " " + r.Path
		classification, ok := auditCoverage[key]
		if !ok {
			t.Errorf("%s is not classified in auditCoverage: add it as audited (and call app.audit/app.auditAdmin in the handler) or exempt with a reason", key)
			continue
		}
		if classification == "audited" {
			if !handlerCallsAudit(t, r.HandlerFunc) {
				t.Errorf("%s is classified audited but its handler never calls app.audit/app.auditAdmin", key)
			}
		} else if !strings.HasPrefix(classification, "exempt:") {
			t.Errorf("%s has an unrecognized classification %q (want \"audited\" or \"exempt: <reason>\")", key, classification)
		}
	}
}
