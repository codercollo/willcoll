package main

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestHealthcheckReportsTheBuild(t *testing.T) {
	oldRelease, oldTime := releaseVersion, buildTime
	t.Cleanup(func() { releaseVersion, buildTime = oldRelease, oldTime })
	releaseVersion, buildTime = "1.4.0", "2026-09-26T10:00:00Z"

	app, _ := newPropertyTestApp(t)
	w := serveAsTenant(app.healthcheckHandler, http.MethodGet, "/v1/healthcheck", "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}

	var body struct {
		Status     string `json:"status"`
		SystemInfo struct {
			Environment string `json:"environment"`
			Version     string `json:"version"`
			BuildTime   string `json:"build_time"`
		} `json:"system_info"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	// Under go test the binary has no VCS stamp, so the version is just the
	// injected release; in a real build it is "1.4.0+<revision>".
	if body.Status != "available" || body.SystemInfo.BuildTime != "2026-09-26T10:00:00Z" ||
		len(body.SystemInfo.Version) < len("1.4.0") || body.SystemInfo.Version[:5] != "1.4.0" {
		t.Errorf("healthcheck = %+v", body)
	}
}
