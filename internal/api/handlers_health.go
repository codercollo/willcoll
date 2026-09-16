package api

import (
	"net/http"

	"github.com/julienschmidt/httprouter"
)

// version and environment are the process-level values GET /healthz reports.
// version is overridable at build time via
//
//	-ldflags "-X github.com/codercollo/willcoll-sys/internal/api.version=1.2.3"
//
// environment defaults to "development" and is overridable the same way (or
// from config once cmd/willcoll wires the server).
var (
	version     = "dev"
	environment = "development"
)

// healthResponse is the hand-encoded JSON body of GET /healthz (no envelope).
type healthResponse struct {
	Status      string `json:"status"`
	Environment string `json:"environment"`
	Version     string `json:"version"`
}

// healthz handles GET /healthz — the liveness/uptime probe hit by the load
// balancer and monitoring (spec §7). It pings the database and reports
// "available" (200) or "unavailable" (503) with the environment and build
// version. The response is encoded directly (no {"data": ...} envelope).
func (s *Server) healthz(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
	resp := healthResponse{
		Status:      "available",
		Environment: environment,
		Version:     version,
	}
	status := http.StatusOK

	if s.pool == nil || s.pool.Ping(r.Context()) != nil {
		resp.Status = "unavailable"
		status = http.StatusServiceUnavailable
	}

	writeJSON(w, status, resp, "")
}
