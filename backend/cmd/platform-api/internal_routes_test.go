package main

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/codercollo/willcoll/backend/internal/shared/auth"
)

// C3.3, the service-to-service actor: /internal/v1/* takes the service
// credential and nothing else.

const (
	svcCurrent  = "svc-current-secret-0123456789"
	svcPrevious = "svc-previous-secret-9876543210"
)

func (f *fakePlatform) service(method, target, body, bearer string, cookie *http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	if cookie != nil {
		req.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	f.router.ServeHTTP(w, req)
	return w
}

const usageBody = `{"manager_id":"00000000-0000-0000-0000-0000000000c1","product":"rentals","billable_count":42}`

func TestServiceEndpointsRefuseAMissingCredential(t *testing.T) {
	f := newFakePlatform(t)
	f.app.config.service = auth.ServiceCredentials{Current: svcCurrent}
	for _, r := range []struct{ method, path, body string }{
		{"GET", "/internal/v1/health", ""},
		{"POST", "/internal/v1/usage", usageBody},
	} {
		if w := f.service(r.method, r.path, r.body, "", nil); w.Code != http.StatusUnauthorized {
			t.Errorf("%s %s without a credential = %d, want 401", r.method, r.path, w.Code)
		}
	}
}

func TestServiceEndpointsRefuseAWrongCredential(t *testing.T) {
	f := newFakePlatform(t)
	f.app.config.service = auth.ServiceCredentials{Current: svcCurrent}
	for _, bad := range []string{"nope", svcCurrent + "x", svcCurrent[:len(svcCurrent)-1], svcPrevious} {
		if w := f.service("POST", "/internal/v1/usage", usageBody, bad, nil); w.Code != http.StatusUnauthorized {
			t.Errorf("credential %q = %d, want 401", bad, w.Code)
		}
	}
	// The credential in the URL is not accepted: it would end up in logs.
	if w := f.service("GET", "/internal/v1/health?token="+svcCurrent, "", "", nil); w.Code != http.StatusUnauthorized {
		t.Errorf("credential in the query string = %d, want 401", w.Code)
	}
}

func TestServiceEndpointsRefuseEverythingWhenNoCredentialIsConfigured(t *testing.T) {
	f := newFakePlatform(t) // config.service is empty
	if w := f.service("GET", "/internal/v1/health", "", "", nil); w.Code != http.StatusUnauthorized {
		t.Errorf("unconfigured, no header = %d, want 401", w.Code)
	}
	// An empty bearer must not match an empty configured secret.
	req := httptest.NewRequest("GET", "/internal/v1/health", nil)
	req.Header.Set("Authorization", "Bearer ")
	w := httptest.NewRecorder()
	f.router.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("unconfigured, empty bearer = %d, want 401", w.Code)
	}
}

func TestServiceCredentialRotationAcceptsThePreviousSecret(t *testing.T) {
	f := newFakePlatform(t)
	f.app.config.service = auth.ServiceCredentials{Current: svcCurrent, Previous: svcPrevious}
	for name, bearer := range map[string]string{"current": svcCurrent, "previous": svcPrevious} {
		if w := f.service("POST", "/internal/v1/usage", usageBody, bearer, nil); w.Code != http.StatusOK {
			t.Errorf("%s credential = %d, want 200: %s", name, w.Code, w.Body)
		}
	}
	// Once the previous value is dropped from the environment it stops working.
	f.app.config.service = auth.ServiceCredentials{Current: svcCurrent}
	if w := f.service("POST", "/internal/v1/usage", usageBody, svcPrevious, nil); w.Code != http.StatusUnauthorized {
		t.Errorf("retired credential = %d, want 401", w.Code)
	}
}

// A signed-in user, whatever their role, is not a service.
func TestUserSessionsCannotCallServiceEndpoints(t *testing.T) {
	f := newFakePlatform(t)
	f.app.config.service = auth.ServiceCredentials{Current: svcCurrent}
	for name, cookie := range map[string]*http.Cookie{
		"firm login":  f.session(t, principalManager, fakeManagerID),
		"staff":       f.session(t, principalStaff, fakeStaffID),
		"super admin": f.session(t, principalAdmin, fakeAdminID),
	} {
		if w := f.service("POST", "/internal/v1/usage", usageBody, "", cookie); w.Code != http.StatusUnauthorized {
			t.Errorf("%s session = %d, want 401", name, w.Code)
		}
	}
}

func TestServiceUsageValidatesItsBody(t *testing.T) {
	f := newFakePlatform(t)
	f.app.config.service = auth.ServiceCredentials{Current: svcCurrent}
	for name, body := range map[string]string{
		"bad manager":     `{"manager_id":"x","product":"rentals","billable_count":1}`,
		"unknown product": `{"manager_id":"00000000-0000-0000-0000-0000000000c1","product":"bogus","billable_count":1}`,
		"negative count":  `{"manager_id":"00000000-0000-0000-0000-0000000000c1","product":"rentals","billable_count":-1}`,
		"missing count":   `{"manager_id":"00000000-0000-0000-0000-0000000000c1","product":"rentals"}`,
	} {
		if w := f.service("POST", "/internal/v1/usage", body, svcCurrent, nil); w.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s = %d, want 422: %s", name, w.Code, w.Body)
		}
	}
}

// The credential never reaches a log line, including the refusal's.
func TestServiceCredentialIsNeverLogged(t *testing.T) {
	f := newFakePlatform(t)
	var logs bytes.Buffer
	f.app.logger = slog.New(slog.NewTextHandler(&logs, nil))
	f.app.config.service = auth.ServiceCredentials{Current: svcCurrent}
	f.service("GET", "/internal/v1/health", "", "wrong-"+svcCurrent, nil)
	f.service("GET", "/internal/v1/health", "", svcCurrent, nil)
	if strings.Contains(logs.String(), svcCurrent) {
		t.Errorf("a log line contains the service credential: %s", logs.String())
	}
}
