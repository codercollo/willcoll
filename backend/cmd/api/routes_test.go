package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// TestBillingRoutesAreRegisteredBehindAuth builds the real router (Gin panics
// on a duplicate route, so this also proves each path is registered once) and
// checks every billing, rent, report and receipt route exists and rejects an
// anonymous caller rather than answering 404 or 501.
func TestBillingRoutesAreRegisteredBehindAuth(t *testing.T) {
	m := newMiddlewareTestApp(t)
	id := uuid.NewString()

	routes := []struct{ method, path string }{
		{"PUT", "/v1/properties/" + id + "/print-theme"},
		{"PUT", "/v1/properties/" + id + "/rent-schedule"},
		{"POST", "/v1/properties/" + id + "/rent/generate?period=2026-09-01"},
		{"GET", "/v1/properties/" + id + "/rent/overview"},
		{"POST", "/v1/units/" + id + "/rent/payments"},
		{"POST", "/v1/units/" + id + "/rent/charges"},
		{"POST", "/v1/ledger-entries/" + id + "/reverse"},
		{"GET", "/v1/units/" + id + "/ledger/rent"},
		{"GET", "/v1/properties/" + id + "/water?period=2026-09"},
		{"PUT", "/v1/properties/" + id + "/water?period=2026-09"},
		{"POST", "/v1/properties/" + id + "/water/generate?period=2026-09"},
		{"GET", "/v1/properties/" + id + "/water/invoices/2026-09.pdf"},
		{"GET", "/v1/properties/" + id + "/garbage/generate?period=2026-09"},
		{"POST", "/v1/properties/" + id + "/garbage/generate?period=2026-09"},
		{"GET", "/v1/properties/" + id + "/garbage/invoices/2026-09.pdf"},
		{"GET", "/v1/properties/" + id + "/reports/2026-09"},
		{"POST", "/v1/properties/" + id + "/reports/2026-09/generate"},
		{"GET", "/v1/properties/" + id + "/receipts/2026-09/download"},
		{"GET", "/v1/account"},
		{"PATCH", "/v1/account"},
		{"PUT", "/v1/account/password"},
		{"POST", "/v1/properties/" + id + "/units/import"},
		{"GET", "/v1/units/" + id + "/leases"},
		{"POST", "/v1/leases/" + id + "/payers"},
		{"DELETE", "/v1/leases/" + id + "/payers/" + id},
		{"GET", "/v1/payments/review"},
		{"POST", "/v1/payments/" + id + "/allocate"},
		{"POST", "/v1/payments/" + id + "/confirm"},
		{"GET", "/v1/billing/plans"},
		{"GET", "/v1/billing/subscription"},
		{"POST", "/v1/billing/subscription/renew"},
		{"GET", "/v1/billing/invoices"},
		{"GET", "/v1/billing/invoices/" + id + "/pdf"},
		// Webhooks are not behind a login: with no secret configured they
		// refuse every caller instead.
		{"POST", "/v1/webhooks/payhero/collections"},
		{"POST", "/v1/webhooks/payhero/subscriptions"},
	}
	for _, r := range routes {
		t.Run(r.method+" "+r.path, func(t *testing.T) {
			w := httptest.NewRecorder()
			m.router.ServeHTTP(w, httptest.NewRequest(r.method, r.path, strings.NewReader("{}")))
			if w.Code != http.StatusUnauthorized {
				t.Errorf("anonymous %s %s = %d, want 401: %s", r.method, r.path, w.Code, w.Body)
			}
		})
	}
}
