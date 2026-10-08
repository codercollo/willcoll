package httpx

import (
	"expvar"
	"log/slog"
	"net/http"
	"time"
)

// NewMetricsServer returns the internal-only listener that serves expvar at
// /debug/vars. It is a separate http.Server on its own address (METRICS_ADDR,
// bound to the container network or loopback and never published or routed by
// the reverse proxy), so the public router has no /debug/* route at all.
// Anything else on it is a 404.
func NewMetricsServer(addr string, logger *slog.Logger) *http.Server {
	mux := http.NewServeMux()
	mux.Handle("GET /debug/vars", expvar.Handler())
	return &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
		ErrorLog:     slog.NewLogLogger(logger.Handler(), slog.LevelError),
	}
}
