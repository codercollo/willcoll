package api

import (
	"context"
	"net/http"
	"strings"

	"github.com/codercollo/willcoll-sys/internal/auth"
)

// contextKey is the unexported key type used for request-context values, so
// values from this package cannot collide with context keys from other packages.
type contextKey string

const claimsContextKey contextKey = "claims"

// authenticate verifies the bearer token, attaches the decoded Claims to the
// request context, and returns 401 if the token is missing or invalid.
func (s *Server) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, ok := bearerToken(r)
		if !ok {
			writeJSONError(w, http.StatusUnauthorized, "invalid or missing authentication token")
			return
		}

		claims, err := s.auth.VerifyToken(token)
		if err != nil {
			writeJSONError(w, http.StatusUnauthorized, "invalid or missing authentication token")
			return
		}

		ctx := context.WithValue(r.Context(), claimsContextKey, claims)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// bearerToken extracts a non-empty token from an Authorization: Bearer header.
func bearerToken(r *http.Request) (string, bool) {
	header := r.Header.Get("Authorization")
	if header == "" {
		return "", false
	}

	parts := strings.SplitN(header, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return "", false
	}

	token := strings.TrimSpace(parts[1])
	if token == "" {
		return "", false
	}

	return token, true
}

// claimsFromContext returns the Claims authenticate stored on the request.
func claimsFromContext(ctx context.Context) (auth.Claims, bool) {
	claims, ok := ctx.Value(claimsContextKey).(auth.Claims)
	return claims, ok
}
