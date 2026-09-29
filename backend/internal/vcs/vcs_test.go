package vcs

import (
	"runtime/debug"
	"testing"
)

func settings(kv ...string) []debug.BuildSetting {
	var out []debug.BuildSetting
	for i := 0; i < len(kv); i += 2 {
		out = append(out, debug.BuildSetting{Key: kv[i], Value: kv[i+1]})
	}
	return out
}

func TestFromSettings(t *testing.T) {
	const full = "3f9c2ab1d0e47a86b5c1d2e3f4a5b6c7d8e9f001"
	tests := []struct {
		name string
		in   []debug.BuildSetting
		want string
	}{
		{"clean tree", settings("vcs.revision", full, "vcs.modified", "false"), "3f9c2ab1d0e4"},
		{"dirty tree", settings("vcs.revision", full, "vcs.modified", "true"), "3f9c2ab1d0e4-dirty"},
		{"short revision is kept whole", settings("vcs.revision", "abc123"), "abc123"},
		{"no vcs information", settings("-compiler", "gc"), ""},
		{"modified without a revision", settings("vcs.modified", "true"), ""},
		{"nothing", nil, ""},
	}
	for _, tc := range tests {
		if got := fromSettings(tc.in); got != tc.want {
			t.Errorf("%s: fromSettings = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestCombine(t *testing.T) {
	for _, tc := range []struct{ release, revision, want string }{
		{"1.4.0", "3f9c2ab1d0e4", "1.4.0+3f9c2ab1d0e4"},
		{"1.4.0", "", "1.4.0"},
		{"", "3f9c2ab1d0e4-dirty", "3f9c2ab1d0e4-dirty"},
		{"  ", "", "dev"},
		{"", "", "dev"},
	} {
		if got := Combine(tc.release, tc.revision); got != tc.want {
			t.Errorf("Combine(%q, %q) = %q, want %q", tc.release, tc.revision, got, tc.want)
		}
	}
}

// Under `go test` the binary has no VCS stamp; Version must say so rather than
// invent one.
func TestVersionWithoutStamp(t *testing.T) {
	if got := Version(); got != "" && len(got) < 7 {
		t.Errorf("Version() = %q, want empty or a real revision", got)
	}
}
