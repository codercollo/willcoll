package main

import (
	"reflect"
	"testing"
)

// TestParseTrustedOrigins is the 2026-09-28 regression: CORS_TRUSTED_ORIGINS
// is documented as space-separated, but a comma-separated value (with or
// without quotes around it, as many .env tools and shells write it) must
// still work rather than silently failing every CORS check.
func TestParseTrustedOrigins(t *testing.T) {
	for in, want := range map[string][]string{
		"":                                         nil,
		"https://a.example":                        {"https://a.example"},
		"https://a.example https://b.example":      {"https://a.example", "https://b.example"},
		"https://a.example,https://b.example":      {"https://a.example", "https://b.example"},
		"https://a.example, https://b.example":     {"https://a.example", "https://b.example"},
		`"https://a.example"`:                      {"https://a.example"},
		`'https://a.example', 'https://b.example'`: {"https://a.example", "https://b.example"},
		"  https://a.example  \t":                  {"https://a.example"},
	} {
		got := parseTrustedOrigins(in)
		if len(got) == 0 {
			got = nil
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("parseTrustedOrigins(%q) = %#v, want %#v", in, got, want)
		}
	}
}
