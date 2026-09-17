// Package scoring is the Go-side client for the Verified Property Score
// ml-sidecar (spec §13). It never computes a score itself — it gates on the
// add-on, calls the sidecar server-to-server, and persists the result.
package scoring

import (
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Service holds the DB pool (for addon gating + property_scores) and the
// sidecar HTTP client config (spec §13.3: shared-secret header, no other
// auth — the sidecar is never reachable from the public internet).
type Service struct {
	pool           *pgxpool.Pool
	sidecarBaseURL string
	sharedSecret   string
	http           *http.Client
}

// NewService constructs a scoring Service. sidecarBaseURL/sharedSecret come
// from config.Scoring (SIDECAR_BASE_URL / SIDECAR_SHARED_SECRET).
func NewService(pool *pgxpool.Pool, sidecarBaseURL, sharedSecret string) *Service {
	return &Service{
		pool:           pool,
		sidecarBaseURL: sidecarBaseURL,
		sharedSecret:   sharedSecret,
		http:           &http.Client{Timeout: 30 * time.Second},
	}
}
