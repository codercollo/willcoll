package auth

import (
	"net/http/httptest"
	"testing"
)

func TestServiceCredentials(t *testing.T) {
	creds := ServiceCredentials{Current: "cur-secret", Previous: "old-secret"}

	cases := []struct {
		name   string
		creds  ServiceCredentials
		header string
		want   bool
	}{
		{"current", creds, "Bearer cur-secret", true},
		{"previous (rotation)", creds, "Bearer old-secret", true},
		{"scheme is case-insensitive", creds, "bearer cur-secret", true},
		{"wrong", creds, "Bearer cur-secreT", false},
		{"prefix of the secret", creds, "Bearer cur-secre", false},
		{"missing header", creds, "", false},
		{"wrong scheme", creds, "Basic cur-secret", false},
		{"empty bearer", creds, "Bearer ", false},
		{"nothing configured", ServiceCredentials{}, "Bearer anything", false},
		{"empty bearer, nothing configured", ServiceCredentials{}, "Bearer ", false},
		{"no previous configured", ServiceCredentials{Current: "cur-secret"}, "Bearer old-secret", false},
	}
	for _, tc := range cases {
		r := httptest.NewRequest("GET", "/", nil)
		if tc.header != "" {
			r.Header.Set("Authorization", tc.header)
		}
		if got := tc.creds.Verify(r); got != tc.want {
			t.Errorf("%s: Verify = %v, want %v", tc.name, got, tc.want)
		}
	}
}
