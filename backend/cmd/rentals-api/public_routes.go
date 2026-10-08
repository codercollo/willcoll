package main

// publicRoutes is the reviewed allowlist of rentals-api routes that carry no
// permission (docs/security/authorization.md, deny by default). The value is
// why the route is public and what protects it. Adding an entry is a security
// decision: it needs review, and auth.Public panics at startup for any public
// route that is not listed here.
var publicRoutes = map[string]string{
	"GET /q/:token":                                   "QR sticker scan redirect; unguessable token, rate limited",
	"GET /v1/healthcheck":                             "liveness probe; no data",
	"GET /v1/pay/:propertySlug/:unitCode":             "tenant pay page; public unit info, rate limited",
	"POST /v1/pay/:propertySlug/:unitCode/otp":        "tenant OTP request; phone must be on the active lease, rate limited",
	"POST /v1/pay/:propertySlug/:unitCode/otp/verify": "tenant OTP check; 5 attempts per code, issues the pay-session token",
	"GET /v1/pay/:propertySlug/:unitCode/balances":    "tenant balances; pay-session token bound to this unit and tenant",
	"POST /v1/pay/:propertySlug/:unitCode/intent":     "tenant payment intent; pay-session token bound to this unit and tenant",
	"GET /v1/pay/intents/:id/status":                  "tenant intent status; pay-session token, looked up by its own tenant and unit",
	"POST /v1/webhooks/payhero/collections":           "PayHero callback; payhero.VerifyWebhook (IP allowlist and/or secret/HMAC), fail-closed",
}
