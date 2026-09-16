package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// statusRecorder captures the response status and byte count so the request
// logger can report what actually went out on the wire.
type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	n, err := r.ResponseWriter.Write(b)
	r.bytes += n
	return n, err
}

// logRequests emits one structured JSON line per request: method, URI, status,
// duration, and remote address.
func (s *Server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		rec := &statusRecorder{ResponseWriter: w}
		next.ServeHTTP(rec, r)

		status := rec.status
		if status == 0 {
			status = http.StatusOK
		}

		s.logger.Info("request",
			"method", r.Method,
			"uri", r.URL.RequestURI(),
			"status", status,
			"bytes", rec.bytes,
			"duration", time.Since(start),
			"remote_addr", r.RemoteAddr,
		)
	})
}

// recoverPanic catches panics from downstream handlers, logs them, and returns
// the enveloped JSON 500 response defined in spec §6.2.
func (s *Server) recoverPanic(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				s.logger.Error("panic recovered",
					"method", r.Method,
					"uri", r.URL.RequestURI(),
					"error", fmt.Sprint(recovered),
				)

				w.Header().Set("Connection", "close")
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusInternalServerError)
				_ = json.NewEncoder(w).Encode(map[string]string{
					"error": "the server encountered a problem and could not process your request",
				})
			}
		}()

		next.ServeHTTP(w, r)
	})
}
