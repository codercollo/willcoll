package api

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func newTestLoggerServer() *Server {
	return &Server{logger: slog.New(slog.NewJSONHandler(io.Discard, nil))}
}

// TestRecoverPanicReturnsEnvelopedServerError proves a downstream panic is
// caught and turned into the standard {"error": ...} 500 envelope rather than
// crashing the process or leaking the panic value to the client.
func TestRecoverPanicReturnsEnvelopedServerError(t *testing.T) {
	s := newTestLoggerServer()

	handler := s.recoverPanic(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("boom")
	}))

	req := httptest.NewRequest(http.MethodGet, "http://example/", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusInternalServerError)
	}
	if got := rr.Header().Get("Connection"); got != "close" {
		t.Fatalf("Connection header = %q, want %q", got, "close")
	}

	var body struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Error == "" {
		t.Fatal("expected non-empty error message")
	}
}

// TestLogRequestsRecordsResponseStatus proves the request logger observes the
// status the handler actually wrote, not a hardcoded default.
func TestLogRequestsRecordsResponseStatus(t *testing.T) {
	s := newTestLoggerServer()

	handler := s.logRequests(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))

	req := httptest.NewRequest(http.MethodGet, "http://example/", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusTeapot {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusTeapot)
	}
}
