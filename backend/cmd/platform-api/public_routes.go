package main

// publicRoutes is the reviewed allowlist of platform-api routes that carry
// no permission (docs/security/authorization.md, deny by default). The value
// is why the route is public and what protects it. Adding an entry is a
// security decision: it needs review, and auth.Public panics at startup for
// any public route that is not listed here.
var publicRoutes = map[string]string{
	"GET /v1/healthcheck":                     "liveness probe; no data",
	"POST /v1/managers":                       "firm signup; rate limited",
	"PUT /v1/managers/activated":              "firm activation; single-use activation token",
	"POST /v1/tokens/activation":              "resend activation email; rate limited, same response for any address",
	"PUT /v1/staff/activated":                 "staff invite acceptance; single-use invite token",
	"POST /v1/sessions":                       "firm/staff login; rate limited, credentials",
	"GET /v1/sessions":                        "whoami; the handler answers 401 to an anonymous caller",
	"DELETE /v1/sessions":                     "logout; ends only the caller's own session",
	"POST /v1/sessions/step-up":               "password re-entry for the caller's own session (D5); 401 anonymous, 403 Super Admin, rate limited, session ended after 5 wrong tries",
	"POST /v1/admin/sessions":                 "Super Admin login; rate limited, credentials",
	"POST /v1/tokens/password-reset":          "password reset email; rate limited, same response for any address",
	"PUT /v1/managers/password":               "password reset completion; single-use reset token, rate limited",
	"GET /internal/v1/health":                 "service-to-service (C3.3): requireService, the SERVICE_TOKEN bearer credential only, never a session; internal network only, blocked at Caddy",
	"POST /internal/v1/usage":                 "service-to-service (C3.3): requireService, the SERVICE_TOKEN bearer credential only, never a session; internal network only, blocked at Caddy",
	"POST /v1/webhooks/payhero/subscriptions": "PayHero callback; payhero.VerifyWebhook (IP allowlist and/or secret/HMAC), fail-closed",
}
