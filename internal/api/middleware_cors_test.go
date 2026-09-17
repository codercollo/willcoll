package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestCORSSetsHeadersOnSimpleRequest proves the defense-in-depth CORS headers
// are present on an ordinary (non-preflight) request and the downstream
// handler still runs.
func TestCORSSetsHeadersOnSimpleRequest(t *testing.T) {
	s := &Server{}

	called := false
	handler := s.cors(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "http://example/v1/properties", nil)
	req.Header.Set("Origin", "http://localhost:3000")
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if !called {
		t.Fatal("expected downstream handler to run for a simple request")
	}
	if got := rr.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("Access-Control-Allow-Origin = %q, want %q", got, "*")
	}
	if got := rr.Header().Get("Vary"); got != "Origin" {
		t.Fatalf("Vary = %q, want %q", got, "Origin")
	}
}

// TestCORSShortCircuitsPreflight proves an OPTIONS preflight is answered
// directly with 204 and never reaches the downstream handler.
func TestCORSShortCircuitsPreflight(t *testing.T) {
	s := &Server{}

	called := false
	handler := s.cors(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))

	req := httptest.NewRequest(http.MethodOptions, "http://example/v1/properties", nil)
	req.Header.Set("Origin", "http://localhost:3000")
	req.Header.Set("Access-Control-Request-Method", "POST")
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if called {
		t.Fatal("expected preflight to short-circuit before the downstream handler")
	}
	if rr.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusNoContent)
	}
	if got := rr.Header().Get("Access-Control-Allow-Methods"); got == "" {
		t.Fatal("expected Access-Control-Allow-Methods to be set on preflight response")
	}
	if got := rr.Header().Get("Access-Control-Allow-Headers"); got == "" {
		t.Fatal("expected Access-Control-Allow-Headers to be set on preflight response")
	}
}
