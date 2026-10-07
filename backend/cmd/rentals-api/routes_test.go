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
		{"POST", "/v1/properties/" + id + "/units/import"},
		{"GET", "/v1/units/" + id + "/leases"},
		{"POST", "/v1/leases/" + id + "/payers"},
		{"DELETE", "/v1/leases/" + id + "/payers/" + id},
		{"GET", "/v1/payments/review"},
		{"POST", "/v1/payments/" + id + "/allocate"},
		{"POST", "/v1/payments/" + id + "/confirm"},
		// Webhooks are not behind a login: with no secret configured they
		// refuse every caller instead.
		{"POST", "/v1/webhooks/payhero/collections"},
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

// TestRetiredIdentityRoutesAreGone pins build-phases.txt 3i-b: rentals-api no
// longer registers, activates, signs in, resets passwords for, or bills
// managers — platform-api owns all of it — so every one of those paths is a
// plain 404 here, never a 401/405 that would suggest it still exists.
func TestRetiredIdentityRoutesAreGone(t *testing.T) {
	m := newMiddlewareTestApp(t)
	id := uuid.NewString()

	routes := []struct{ method, path string }{
		{"POST", "/v1/managers"},
		{"PUT", "/v1/managers/activated"},
		{"PUT", "/v1/managers/password"},
		{"POST", "/v1/tokens/activation"},
		{"POST", "/v1/tokens/password-reset"},
		{"POST", "/v1/sessions"},
		{"GET", "/v1/sessions"},
		{"DELETE", "/v1/sessions"},
		{"POST", "/v1/admin/sessions"},
		{"GET", "/v1/account"},
		{"PATCH", "/v1/account"},
		{"PUT", "/v1/account/password"},
		{"GET", "/v1/billing/plans"},
		{"GET", "/v1/billing/subscription"},
		{"POST", "/v1/billing/subscription/renew"},
		{"GET", "/v1/billing/invoices"},
		{"GET", "/v1/billing/invoices/" + id + "/pdf"},
		{"POST", "/v1/webhooks/payhero/subscriptions"},
		{"GET", "/v1/admin/managers"},
		{"GET", "/v1/admin/managers/" + id},
		{"POST", "/v1/admin/managers/" + id + "/suspend"},
		{"POST", "/v1/admin/managers/" + id + "/reinstate"},
		{"GET", "/v1/admin/subscriptions"},
		{"GET", "/v1/admin/plans"},
		{"POST", "/v1/admin/plans"},
		{"PATCH", "/v1/admin/plans/" + id},
		{"POST", "/v1/admin/plans/" + id + "/archive"},
		{"POST", "/v1/admin/plans/" + id + "/restore"},
	}
	for _, r := range routes {
		t.Run(r.method+" "+r.path, func(t *testing.T) {
			w := httptest.NewRecorder()
			m.router.ServeHTTP(w, httptest.NewRequest(r.method, r.path, strings.NewReader("{}")))
			if w.Code != http.StatusNotFound {
				t.Errorf("%s %s = %d, want 404: %s", r.method, r.path, w.Code, w.Body)
			}
		})
	}
}
