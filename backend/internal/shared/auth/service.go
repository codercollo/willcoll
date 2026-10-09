package auth

import (
	"crypto/sha256"
	"crypto/subtle"
	"net/http"
	"strings"
)

// Service-to-service credential (docs/security/c3-spec.md C3.3): product APIs
// call platform-api's /internal/v1/* endpoints with a per-service secret, not
// a user session. The secret lives in the environment of both processes
// (SERVICE_TOKEN), is sent only as `Authorization: Bearer <secret>` (never in
// a URL, so it cannot reach an access log), and is compared in constant time.
//
// Rotation: deploy the new secret as SERVICE_TOKEN with the old one as
// SERVICE_TOKEN_PREVIOUS (both accepted), roll the callers to the new one,
// then drop SERVICE_TOKEN_PREVIOUS.

// ServiceCredentials is the pair of secrets platform-api accepts.
type ServiceCredentials struct {
	Current  string
	Previous string
}

// Enabled reports whether any secret is configured. With none, every service
// request is refused: there is no unauthenticated mode.
func (s ServiceCredentials) Enabled() bool { return s.Current != "" }

// Verify reports whether r carries a configured secret as a bearer token.
func (s ServiceCredentials) Verify(r *http.Request) bool {
	const prefix = "Bearer "
	header := r.Header.Get("Authorization")
	if len(header) <= len(prefix) || !strings.EqualFold(header[:len(prefix)], prefix) {
		return false
	}
	return s.Matches(strings.TrimSpace(header[len(prefix):]))
}

// Matches compares a presented secret against Current and Previous. Both
// comparisons always run, over fixed-length digests, so timing reveals
// neither which secret matched nor how long the real one is.
func (s ServiceCredentials) Matches(presented string) bool {
	if presented == "" || s.Current == "" {
		return false
	}
	p := sha256.Sum256([]byte(presented))
	cur := sha256.Sum256([]byte(s.Current))
	ok := subtle.ConstantTimeCompare(p[:], cur[:])
	if s.Previous != "" {
		prev := sha256.Sum256([]byte(s.Previous))
		ok |= subtle.ConstantTimeCompare(p[:], prev[:])
	}
	return ok == 1
}
