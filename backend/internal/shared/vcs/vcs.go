// Package vcs reports which commit a binary was built from, so a running
// server can say exactly what it is (GET /v1/healthcheck, /debug/vars).
package vcs

import (
	"runtime/debug"
	"strings"
)

// Version returns the VCS revision the binary was built from, shortened, with
// "-dirty" appended when the working tree had uncommitted changes. It returns
// "" when the binary carries no VCS information: a build outside a checkout,
// `go run`, or `go test`.
func Version() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	return fromSettings(info.Settings)
}

// fromSettings reads the vcs.* build settings the go tool stamps into a
// binary (Go 1.18+).
func fromSettings(settings []debug.BuildSetting) string {
	var revision string
	var modified bool
	for _, s := range settings {
		switch s.Key {
		case "vcs.revision":
			revision = s.Value
		case "vcs.modified":
			modified = s.Value == "true"
		}
	}
	if revision == "" {
		return ""
	}
	if len(revision) > 12 {
		revision = revision[:12]
	}
	if modified {
		revision += "-dirty"
	}
	return revision
}

// Combine joins a release version injected at link time (-ldflags "-X
// main.version=...") with the VCS revision, as "1.4.0+3f9c2ab1d0e4". Either
// may be empty; with neither, it is "dev".
func Combine(release, revision string) string {
	release = strings.TrimSpace(release)
	switch {
	case release != "" && revision != "":
		return release + "+" + revision
	case release != "":
		return release
	case revision != "":
		return revision
	default:
		return "dev"
	}
}
