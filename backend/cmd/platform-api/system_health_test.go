package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestCheckProductService(t *testing.T) {
	serve := func(code int, body string, delay time.Duration) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(delay)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(code)
			_, _ = w.Write([]byte(body))
		}))
	}
	check := func(url string, timeout time.Duration) envelope {
		return checkProductService(context.Background(), timeout, url, "willcoll_session=abc")
	}

	t.Run("no URL configured is not an outage", func(t *testing.T) {
		if got := check("", time.Second)["status"]; got != "not_configured" {
			t.Errorf("status = %v", got)
		}
	})

	t.Run("an available product is ok and carries its report", func(t *testing.T) {
		srv := serve(http.StatusOK, `{"status":"available","database":{"status":"ok"}}`, 0)
		defer srv.Close()
		got := check(srv.URL, time.Second)
		if got["status"] != "ok" || got["report"] == nil {
			t.Errorf("got %v", got)
		}
	})

	t.Run("a product reporting degraded stays degraded, with its report", func(t *testing.T) {
		srv := serve(http.StatusOK, `{"status":"degraded"}`, 0)
		defer srv.Close()
		if got := check(srv.URL, time.Second); got["status"] != "degraded" || got["report"] == nil {
			t.Errorf("got %v", got)
		}
	})

	t.Run("a product whose own database is down is unreachable", func(t *testing.T) {
		srv := serve(http.StatusServiceUnavailable, `{"status":"unavailable"}`, 0)
		defer srv.Close()
		if got := check(srv.URL, time.Second)["status"]; got != "unreachable" {
			t.Errorf("status = %v", got)
		}
	})

	t.Run("a refused session is unauthorized, not down", func(t *testing.T) {
		for _, code := range []int{http.StatusUnauthorized, http.StatusForbidden} {
			srv := serve(code, `{"error":"no"}`, 0)
			if got := check(srv.URL, time.Second)["status"]; got != "unauthorized" {
				t.Errorf("%d: status = %v", code, got)
			}
			srv.Close()
		}
	})

	t.Run("a slow product times out quickly instead of holding the page", func(t *testing.T) {
		srv := serve(http.StatusOK, `{"status":"available"}`, 500*time.Millisecond)
		defer srv.Close()
		start := time.Now()
		got := check(srv.URL, 50*time.Millisecond)
		if got["status"] != "unreachable" || got["error"] != "timed out" {
			t.Errorf("got %v", got)
		}
		if elapsed := time.Since(start); elapsed > 400*time.Millisecond {
			t.Errorf("took %v, want about the 50ms timeout", elapsed)
		}
	})

	t.Run("a product that is not listening is unreachable", func(t *testing.T) {
		srv := serve(http.StatusOK, `{}`, 0)
		url := srv.URL
		srv.Close()
		if got := check(url, time.Second)["status"]; got != "unreachable" {
			t.Errorf("status = %v", got)
		}
	})

	t.Run("the admin's session cookie is forwarded", func(t *testing.T) {
		var gotCookie, gotPath string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotCookie, gotPath = r.Header.Get("Cookie"), r.URL.Path
			_, _ = w.Write([]byte(`{"status":"available"}`))
		}))
		defer srv.Close()
		check(srv.URL, time.Second)
		if gotCookie != "willcoll_session=abc" || gotPath != "/v1/admin/system/health" {
			t.Errorf("cookie %q path %q", gotCookie, gotPath)
		}
	})
}
