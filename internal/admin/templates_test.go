package admin

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/codercollo/willcoll-sys/internal/money"
	"github.com/google/uuid"
)

func assertRendered(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	if rec.Code >= 400 || strings.Contains(rec.Body.String(), "render error") {
		t.Fatalf("template render failed: status=%d body=%s", rec.Code, rec.Body.String())
	}
}

// TestTemplatesRender is a smoke test only — it has no live Postgres in CI
// here, so it just proves every template.Must(...Parse(...)) in this
// package doesn't panic and every field referenced by a template actually
// exists on the struct it's fed (html/template catches unknown fields at
// Execute time, not Parse time).
func TestTemplatesRender(t *testing.T) {
	rec := httptest.NewRecorder()
	renderPageWithNav(rec, "t", loginTmpl, map[string]any{"Error": "x"}, false)
	assertRendered(t, rec)

	rec = httptest.NewRecorder()
	renderPage(rec, "t", dashboardTmpl, map[string]any{})
	assertRendered(t, rec)

	rec = httptest.NewRecorder()
	renderPage(rec, "t", organizationsTmpl, map[string]any{
		"Orgs": []Organization{{ID: uuid.New(), Name: "Acme", SubscriptionTier: "starter", BillingStatus: "active", UnitCount: 3, CreatedAt: time.Now()}},
	})
	assertRendered(t, rec)

	rec = httptest.NewRecorder()
	renderPage(rec, "t", addonsTmpl, map[string]any{
		"Subs": []AddonSubscriber{{OrganizationID: uuid.New(), OrganizationName: "Acme", MonthlyFeeKES: 5000, ActivatedAt: time.Now()}},
	})
	assertRendered(t, rec)

	rec = httptest.NewRecorder()
	renderPage(rec, "t", auditLogTmpl, map[string]any{
		"Entries": []AuditEntry{{ID: uuid.New(), OrganizationID: uuid.New(), Action: "organization.updated", EntityType: "organization", Source: "platform_admin", CreatedAt: time.Now()}},
	})
	assertRendered(t, rec)

	rec = httptest.NewRecorder()
	renderPage(rec, "t", operationsTmpl, map[string]any{
		"Drift": []money.ReconcileDrift{{AccountID: uuid.New(), OrganizationID: uuid.New(), Balance: 100, EntrySum: 90}},
		"Stuck": []StuckGatewayTransaction{{ID: uuid.New(), OrganizationID: uuid.New(), GatewayRef: "ref-1", Channel: "mpesa", Amount: "500.00", ReceivedAt: time.Now()}},
	})
	assertRendered(t, rec)
}
