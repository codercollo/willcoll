# Route x role matrix

Generated from `backend/internal/shared/auth/testdata/route_matrix.golden` by the route-matrix tests (`go test ./cmd/platform-api ./cmd/rentals-api -run RouteMatrix -update`). Do not edit by hand.

Every route is called as every principal against a real database with a seeded fixture (`docs/security/c4-spec.md` C4.2, `docs/security/c4-matrix-fixes.md`). A row `METHOD /path [variant]` is the same route called with a specific, realistic body, for routes whose rule depends on what is being changed.

| Cell | Meaning |
|---|---|
| `2xx` | the request reached the handler and succeeded |
| `404h` `422h` `409h` `400h` `503h` ... | **the handler ran** (authorization let the request through) and returned that code |
| `404` | refused by scope: out of the caller's properties or firm; the handler never ran |
| `403` | refused by permission |
| `401` | not authenticated |
| `step-up` | refused until the caller re-enters their password |
| `step-up → 2xx*` | refused until step-up; `*` = the same request, repeated with a fresh step-up window, then gave that outcome |
| `-` | not exercised |

`officer_scoped` is restricted to property A and asks for property B's resources. `pay_token` is a valid tenant pay token for unit A1.

## platform-api

| Route | anonymous | pay_token | viewer | officer | officer_scoped | accountant | staff_owner | firm_login | super_admin | service |
|---|---|---|---|---|---|---|---|---|---|---|
| **account** | | | | | | | | | | |
| `GET /v1/account` | 401 | 401 | 2xx | 2xx | 2xx | 2xx | 2xx | 2xx | 403 | 401 |
| `PATCH /v1/account` | 401 | 401 | 403 | 403 | 403 | 403 | 2xx | 2xx | 403 | 401 |
| `PUT /v1/account/password` | 401 | 401 | 403 | 403 | 403 | 403 | 403 | 422h | 403 | 401 |
| **admin/managers** | | | | | | | | | | |
| `GET /v1/admin/managers` | 401 | 401 | 403 | 403 | 403 | 403 | 403 | 403 | 2xx | 401 |
| `GET /v1/admin/managers/:id` | 401 | 401 | 403 | 403 | 403 | 403 | 403 | 403 | 2xx | 401 |
| `POST /v1/admin/managers/:id/reinstate` | 401 | 401 | 403 | 403 | 403 | 403 | 403 | 403 | step-up → 2xx* | 401 |
| `POST /v1/admin/managers/:id/suspend` | 401 | 401 | 403 | 403 | 403 | 403 | 403 | 403 | step-up → 2xx* | 401 |
| `POST /v1/admin/managers/:id/suspend [with reason]` | 401 | 401 | 403 | 403 | 403 | 403 | 403 | 403 | step-up → 2xx* | 401 |
| **admin/plans** | | | | | | | | | | |
| `GET /v1/admin/plans` | 401 | 401 | 403 | 403 | 403 | 403 | 403 | 403 | 2xx | 401 |
| `PATCH /v1/admin/plans/:id` | 401 | 401 | 403 | 403 | 403 | 403 | 403 | 403 | step-up → 2xx* | 401 |
| `PATCH /v1/admin/plans/:id [rename]` | 401 | 401 | 403 | 403 | 403 | 403 | 403 | 403 | step-up → 2xx* | 401 |
| `POST /v1/admin/plans` | 401 | 401 | 403 | 403 | 403 | 403 | 403 | 403 | step-up → 422h* | 401 |
| `POST /v1/admin/plans [create plan]` | 401 | 401 | 403 | 403 | 403 | 403 | 403 | 403 | step-up → 2xx* | 401 |
| `POST /v1/admin/plans/:id/archive` | 401 | 401 | 403 | 403 | 403 | 403 | 403 | 403 | step-up → 2xx* | 401 |
| `POST /v1/admin/plans/:id/restore` | 401 | 401 | 403 | 403 | 403 | 403 | 403 | 403 | step-up → 404h* | 401 |
| **admin/sessions** | | | | | | | | | | |
| `POST /v1/admin/sessions` | 422h | 422h | 422h | 422h | 422h | 422h | 422h | 422h | 422h | 422h |
| **admin/subscriptions** | | | | | | | | | | |
| `GET /v1/admin/subscriptions` | 401 | 401 | 403 | 403 | 403 | 403 | 403 | 403 | 2xx | 401 |
| **admin/system** | | | | | | | | | | |
| `GET /v1/admin/system/health` | 401 | 401 | 403 | 403 | 403 | 403 | 403 | 403 | 2xx | 401 |
| **admin/unclaimed-receipts** | | | | | | | | | | |
| `GET /v1/admin/unclaimed-receipts` | 401 | 401 | 403 | 403 | 403 | 403 | 403 | 403 | 2xx | 401 |
| `POST /v1/admin/unclaimed-receipts/:id/resolve` | 401 | 401 | 403 | 403 | 403 | 403 | 403 | 403 | step-up → 422h* | 401 |
| `POST /v1/admin/unclaimed-receipts/:id/resolve [with note]` | 401 | 401 | 403 | 403 | 403 | 403 | 403 | 403 | step-up → 2xx* | 401 |
| **billing** | | | | | | | | | | |
| `GET /v1/billing/:product/invoices` | 401 | 401 | 403 | 403 | 403 | 403 | 2xx | 2xx | 403 | 401 |
| `GET /v1/billing/:product/invoices/:id/pdf` | 401 | 401 | 403 | 403 | 403 | 403 | 404h | 404h | 403 | 401 |
| `GET /v1/billing/:product/plans` | 401 | 401 | 403 | 403 | 403 | 403 | 2xx | 2xx | 403 | 401 |
| `GET /v1/billing/:product/subscription` | 401 | 401 | 403 | 403 | 403 | 403 | 404h | 404h | 403 | 401 |
| `POST /v1/billing/:product/subscription/renew` | 401 | 401 | 403 | 403 | 403 | 403 | 503h | 503h | 403 | 401 |
| **healthcheck** | | | | | | | | | | |
| `GET /v1/healthcheck` | 2xx | 2xx | 2xx | 2xx | 2xx | 2xx | 2xx | 2xx | 2xx | 2xx |
| **internal** | | | | | | | | | | |
| `GET /internal/v1/health` | 401 | 401 | 401 | 401 | 401 | 401 | 401 | 401 | 401 | 2xx |
| `POST /internal/v1/usage` | 401 | 401 | 401 | 401 | 401 | 401 | 401 | 401 | 401 | 422h |
| **managers** | | | | | | | | | | |
| `POST /v1/managers` | 422h | 422h | 422h | 422h | 422h | 422h | 422h | 422h | 422h | 422h |
| `PUT /v1/managers/activated` | 422h | 422h | 422h | 422h | 422h | 422h | 422h | 422h | 422h | 422h |
| `PUT /v1/managers/password` | 422h | 422h | 422h | 422h | 422h | 422h | 422h | 422h | 422h | 422h |
| **modules** | | | | | | | | | | |
| `GET /v1/modules` | 401 | 401 | 2xx | 2xx | 2xx | 2xx | 2xx | 2xx | 403 | 401 |
| `POST /v1/modules/:product/trial` | 401 | 401 | 403 | 403 | 403 | 403 | 2xx | 2xx | 403 | 401 |
| **sessions** | | | | | | | | | | |
| `DELETE /v1/sessions` | 2xx | 2xx | 2xx | 2xx | 2xx | 2xx | 2xx | 2xx | 2xx | 2xx |
| `GET /v1/sessions` | 401 | 401 | 2xx | 2xx | 2xx | 2xx | 2xx | 2xx | 2xx | 401 |
| `POST /v1/sessions` | 422h | 422h | 422h | 422h | 422h | 422h | 422h | 422h | 422h | 422h |
| `POST /v1/sessions/step-up` | 401 | 401 | 422h | 422h | 422h | 422h | 422h | 422h | 422h | 401 |
| **staff** | | | | | | | | | | |
| `GET /v1/staff` | 401 | 401 | 403 | 403 | 403 | 403 | 2xx | 2xx | 403 | 401 |
| `PATCH /v1/staff/:id` | 401 | 401 | 403 | 403 | 403 | 403 | 2xx | 2xx | 403 | 401 |
| `PATCH /v1/staff/:id [deactivate]` | 401 | 401 | 403 | 403 | 403 | 403 | step-up → 2xx* | step-up → 2xx* | 403 | 401 |
| `PATCH /v1/staff/:id [grant owner]` | 401 | 401 | 403 | 403 | 403 | 403 | 403 | step-up → 2xx* | 403 | 401 |
| `PATCH /v1/staff/:id [module_access change]` | 401 | 401 | 403 | 403 | 403 | 403 | step-up → 2xx* | step-up → 2xx* | 403 | 401 |
| `PATCH /v1/staff/:id [name only]` | 401 | 401 | 403 | 403 | 403 | 403 | 2xx | 2xx | 403 | 401 |
| `PATCH /v1/staff/:id [property_access change]` | 401 | 401 | 403 | 403 | 403 | 403 | step-up → 2xx* | step-up → 2xx* | 403 | 401 |
| `PATCH /v1/staff/:id [role change]` | 401 | 401 | 403 | 403 | 403 | 403 | step-up → 2xx* | step-up → 2xx* | 403 | 401 |
| `POST /v1/staff` | 401 | 401 | 403 | 403 | 403 | 403 | 422h | 422h | 403 | 401 |
| `POST /v1/staff [invite owner]` | 401 | 401 | 403 | 403 | 403 | 403 | 403 | step-up → 2xx* | 403 | 401 |
| `POST /v1/staff [invite viewer]` | 401 | 401 | 403 | 403 | 403 | 403 | step-up → 2xx* | step-up → 2xx* | 403 | 401 |
| `PUT /v1/staff/activated` | 422h | 422h | 422h | 422h | 422h | 422h | 422h | 422h | 422h | 422h |
| `PUT /v1/staff/me/password` | 401 | 401 | 422h | 422h | 422h | 422h | 422h | 403 | 403 | 401 |
| **tokens** | | | | | | | | | | |
| `POST /v1/tokens/activation` | 422h | 422h | 422h | 422h | 422h | 422h | 422h | 422h | 422h | 422h |
| `POST /v1/tokens/password-reset` | 422h | 422h | 422h | 422h | 422h | 422h | 422h | 422h | 422h | 422h |
| **webhooks** | | | | | | | | | | |
| `POST /v1/webhooks/payhero/subscriptions` | 401 | 401 | 401 | 401 | 401 | 401 | 401 | 401 | 401 | 401 |

## rentals-api

| Route | anonymous | pay_token | viewer | officer | officer_scoped | accountant | staff_owner | firm_login | super_admin | service |
|---|---|---|---|---|---|---|---|---|---|---|
| **admin/backups** | | | | | | | | | | |
| `GET /v1/admin/backups` | 401 | 401 | 403 | 403 | 403 | 403 | 403 | 403 | 2xx | 401 |
| **admin/system** | | | | | | | | | | |
| `GET /v1/admin/system/health` | 401 | 401 | 403 | 403 | 403 | 403 | 403 | 403 | 2xx | 401 |
| **healthcheck** | | | | | | | | | | |
| `GET /v1/healthcheck` | 2xx | 2xx | 2xx | 2xx | 2xx | 2xx | 2xx | 2xx | 2xx | 2xx |
| **landlords** | | | | | | | | | | |
| `GET /v1/landlords` | 401 | 401 | 2xx | 2xx | 2xx | 2xx | 2xx | 2xx | 403 | 401 |
| `GET /v1/landlords/:id` | 401 | 401 | 2xx | 2xx | 404 | 2xx | 2xx | 2xx | 403 | 401 |
| `PATCH /v1/landlords/:id` | 401 | 401 | 403 | 403 | 404 | 2xx | 2xx | 2xx | 403 | 401 |
| `PATCH /v1/landlords/:id [name only]` | 401 | 401 | 403 | 403 | 404 | 2xx | 2xx | 2xx | 403 | 401 |
| `PATCH /v1/landlords/:id [payout change]` | 401 | 401 | 403 | 403 | 404 | 403 | step-up → 2xx* | step-up → 2xx* | 403 | 401 |
| `PATCH /v1/landlords/:id [payout echo]` | 401 | 401 | 403 | 403 | 404 | 2xx | 2xx | 2xx | 403 | 401 |
| `POST /v1/landlords` | 401 | 401 | 403 | 403 | 403 | 422h | 422h | 422h | 403 | 401 |
| `POST /v1/landlords [new landlord]` | 401 | 401 | 403 | 403 | 403 | 2xx | 2xx | 2xx | 403 | 401 |
| `POST /v1/landlords [with payout]` | 401 | 401 | 403 | 403 | 403 | 403 | step-up → 2xx* | step-up → 2xx* | 403 | 401 |
| **leases** | | | | | | | | | | |
| `DELETE /v1/leases/:id/payers/:payerId` | 401 | 401 | 403 | 403 | 404 | 2xx | 2xx | 2xx | 403 | 401 |
| `GET /v1/leases/:id` | 401 | 401 | 2xx | 2xx | 404 | 2xx | 2xx | 2xx | 403 | 401 |
| `GET /v1/leases/:id/qr` | 401 | 401 | 403 | 503h | 404 | 503h | 503h | 503h | 403 | 401 |
| `GET /v1/leases/:id/qr.png` | 401 | 401 | 403 | 503h | 404 | 503h | 503h | 503h | 403 | 401 |
| `PATCH /v1/leases/:id` | 401 | 401 | 403 | 403 | 404 | 2xx | 2xx | 2xx | 403 | 401 |
| `POST /v1/leases/:id/payers` | 401 | 401 | 403 | 422h | 404 | 422h | 422h | 422h | 403 | 401 |
| `POST /v1/leases/:id/payers [add payer]` | 401 | 401 | 403 | 2xx | 404 | 2xx | 2xx | 2xx | 403 | 401 |
| `POST /v1/leases/:id/qr/rotate` | 401 | 401 | 403 | 403 | 404 | 503h | 503h | 503h | 403 | 401 |
| **ledger-entries** | | | | | | | | | | |
| `POST /v1/ledger-entries/:id/reverse` | 401 | 401 | 403 | 403 | 404 | step-up → 422h* | step-up → 422h* | step-up → 422h* | 403 | 401 |
| `POST /v1/ledger-entries/:id/reverse [with reason]` | 401 | 401 | 403 | 403 | 404 | step-up → 404h* | step-up → 404h* | step-up → 404h* | 403 | 401 |
| **onboarding** | | | | | | | | | | |
| `GET /v1/onboarding` | 401 | 401 | 2xx | 2xx | 2xx | 2xx | 2xx | 2xx | 403 | 401 |
| **pay** | | | | | | | | | | |
| `GET /v1/pay/:propertySlug/:unitCode` | 2xx | 2xx | 2xx | 2xx | 2xx | 2xx | 2xx | 2xx | 2xx | 2xx |
| `GET /v1/pay/:propertySlug/:unitCode/balances` | 401 | 2xx | 401 | 401 | 401 | 401 | 401 | 401 | 401 | 401 |
| `GET /v1/pay/intents/:id/status` | 401 | 404h | 401 | 401 | 401 | 401 | 401 | 401 | 401 | 401 |
| `POST /v1/pay/:propertySlug/:unitCode/intent` | 401 | 422h | 401 | 401 | 401 | 401 | 401 | 401 | 401 | 401 |
| `POST /v1/pay/:propertySlug/:unitCode/otp` | 422h | 422h | 422h | 422h | 422h | 422h | 422h | 422h | 422h | 422h |
| `POST /v1/pay/:propertySlug/:unitCode/otp/verify` | 422h | 422h | 422h | 422h | 422h | 422h | 422h | 422h | 422h | 422h |
| **payment-channel-banks** | | | | | | | | | | |
| `GET /v1/payment-channel-banks` | 401 | 401 | 2xx | 2xx | 2xx | 2xx | 2xx | 2xx | 403 | 401 |
| **payments** | | | | | | | | | | |
| `GET /v1/payments/review` | 401 | 401 | 403 | 2xx | 2xx | 2xx | 2xx | 2xx | 403 | 401 |
| `POST /v1/payments/:id/allocate` | 401 | 401 | 403 | 403 | 404 | 422h | 422h | 422h | 403 | 401 |
| `POST /v1/payments/:id/confirm` | 401 | 401 | 403 | 403 | 404 | 409h | 409h | 409h | 403 | 401 |
| `POST /v1/payments/:id/confirm [confirm pending]` | 401 | 401 | 403 | 403 | 404 | 2xx | 2xx | 2xx | 403 | 401 |
| `POST /v1/payments/:id/reject` | 401 | 401 | 403 | 403 | 404 | 422h | 422h | 422h | 403 | 401 |
| `POST /v1/payments/:id/reject [with reason]` | 401 | 401 | 403 | 403 | 404 | 2xx | 2xx | 2xx | 403 | 401 |
| **properties** | | | | | | | | | | |
| `DELETE /v1/properties/:id` | 401 | 401 | 403 | 403 | 404 | 403 | 409h | 409h | 403 | 401 |
| `GET /v1/properties` | 401 | 401 | 2xx | 2xx | 2xx | 2xx | 2xx | 2xx | 403 | 401 |
| `GET /v1/properties/:id` | 401 | 401 | 2xx | 2xx | 404 | 2xx | 2xx | 2xx | 403 | 401 |
| `GET /v1/properties/:id/electricity/deposits` | 401 | 401 | 403 | 2xx | 404 | 2xx | 2xx | 2xx | 403 | 401 |
| `GET /v1/properties/:id/electricity/deposits.csv` | 401 | 401 | 403 | 403 | 404 | 2xx | 2xx | 2xx | 403 | 401 |
| `GET /v1/properties/:id/electricity/deposits.pdf` | 401 | 401 | 403 | 403 | 404 | 2xx | 2xx | 2xx | 403 | 401 |
| `GET /v1/properties/:id/garbage/generate` | 401 | 401 | 403 | 2xx | 404 | 2xx | 2xx | 2xx | 403 | 401 |
| `GET /v1/properties/:id/garbage/invoices/:period` | 401 | 401 | 403 | 403 | 404 | 422h | 422h | 422h | 403 | 401 |
| `GET /v1/properties/:id/payment-channel/status` | 401 | 401 | 2xx | 2xx | 404 | 2xx | 2xx | 2xx | 403 | 401 |
| `GET /v1/properties/:id/receipts/:period/download` | 401 | 401 | 403 | 403 | 404 | 422h | 422h | 422h | 403 | 401 |
| `GET /v1/properties/:id/rent/overview` | 401 | 401 | 2xx | 2xx | 404 | 2xx | 2xx | 2xx | 403 | 401 |
| `GET /v1/properties/:id/reports/:period` | 401 | 401 | 2xx | 2xx | 404 | 2xx | 2xx | 2xx | 403 | 401 |
| `GET /v1/properties/:id/reports/:period/checks` | 401 | 401 | 2xx | 2xx | 404 | 2xx | 2xx | 2xx | 403 | 401 |
| `GET /v1/properties/:id/reports/:period/plot-meter` | 401 | 401 | 2xx | 2xx | 404 | 2xx | 2xx | 2xx | 403 | 401 |
| `GET /v1/properties/:id/units` | 401 | 401 | 2xx | 2xx | 404 | 2xx | 2xx | 2xx | 403 | 401 |
| `GET /v1/properties/:id/water` | 401 | 401 | 422h | 422h | 404 | 422h | 422h | 422h | 403 | 401 |
| `GET /v1/properties/:id/water/invoices/:period` | 401 | 401 | 403 | 403 | 404 | 404h | 404h | 404h | 403 | 401 |
| `PATCH /v1/properties/:id` | 401 | 401 | 403 | 403 | 404 | 2xx | 2xx | 2xx | 403 | 401 |
| `PATCH /v1/properties/:id [channel change]` | 401 | 401 | 403 | 403 | 404 | 403 | step-up → 2xx* | step-up → 2xx* | 403 | 401 |
| `PATCH /v1/properties/:id [channel echo]` | 401 | 401 | 403 | 403 | 404 | 2xx | 2xx | 2xx | 403 | 401 |
| `PATCH /v1/properties/:id [name only]` | 401 | 401 | 403 | 403 | 404 | 2xx | 2xx | 2xx | 403 | 401 |
| `PATCH /v1/properties/:id/electricity` | 401 | 401 | 403 | 403 | 404 | 2xx | 2xx | 2xx | 403 | 401 |
| `PATCH /v1/properties/:id/garbage` | 401 | 401 | 403 | 403 | 404 | 2xx | 2xx | 2xx | 403 | 401 |
| `POST /v1/properties` | 401 | 401 | 403 | 403 | 403 | 422h | 422h | 422h | 403 | 401 |
| `POST /v1/properties [no channel]` | 401 | 401 | 403 | 403 | 403 | 2xx | 2xx | 2xx | 403 | 401 |
| `POST /v1/properties [with channel]` | 401 | 401 | 403 | 403 | 403 | 403 | step-up → 2xx* | step-up → 2xx* | 403 | 401 |
| `POST /v1/properties/:id/garbage/generate` | 401 | 401 | 403 | 403 | 404 | 422h | 422h | 422h | 403 | 401 |
| `POST /v1/properties/:id/onboarding/import` | 401 | 401 | 403 | 403 | 404 | 422h | 422h | 422h | 403 | 401 |
| `POST /v1/properties/:id/payment-channel` | 401 | 401 | 403 | 403 | 404 | 403 | step-up → 422h* | step-up → 422h* | 403 | 401 |
| `POST /v1/properties/:id/payment-channel/test` | 401 | 401 | 403 | 403 | 404 | 403 | step-up → 422h* | step-up → 422h* | 403 | 401 |
| `POST /v1/properties/:id/qr/bulk` | 401 | 401 | 403 | 403 | 404 | 503h | 503h | 503h | 403 | 401 |
| `POST /v1/properties/:id/rent/generate` | 401 | 401 | 403 | 403 | 404 | 422h | 422h | 422h | 403 | 401 |
| `POST /v1/properties/:id/reports/:period/confirm` | 401 | 401 | 403 | 403 | 404 | 2xx | 2xx | 2xx | 403 | 401 |
| `POST /v1/properties/:id/reports/:period/generate` | 401 | 401 | 403 | 403 | 404 | 409h | 409h | 409h | 403 | 401 |
| `POST /v1/properties/:id/units` | 401 | 401 | 403 | 422h | 404 | 422h | 422h | 422h | 403 | 401 |
| `POST /v1/properties/:id/units [no money fields]` | 401 | 401 | 403 | 2xx | 404 | 2xx | 2xx | 2xx | 403 | 401 |
| `POST /v1/properties/:id/units [with rent]` | 401 | 401 | 403 | 403 | 404 | 422h | 422h | 422h | 403 | 401 |
| `POST /v1/properties/:id/units/import` | 401 | 401 | 403 | 403 | 404 | 422h | 422h | 422h | 403 | 401 |
| `POST /v1/properties/:id/water/generate` | 401 | 401 | 403 | 403 | 404 | 422h | 422h | 422h | 403 | 401 |
| `PUT /v1/properties/:id/print-theme` | 401 | 401 | 403 | 403 | 404 | 2xx | 2xx | 2xx | 403 | 401 |
| `PUT /v1/properties/:id/rent-schedule` | 401 | 401 | 403 | 403 | 404 | 400h | 400h | 400h | 403 | 401 |
| `PUT /v1/properties/:id/rent-schedule [set rent]` | 401 | 401 | 403 | 403 | 404 | 2xx | 2xx | 2xx | 403 | 401 |
| `PUT /v1/properties/:id/reports/:period/plot-meter` | 401 | 401 | 403 | 422h | 404 | 422h | 422h | 422h | 403 | 401 |
| `PUT /v1/properties/:id/water` | 401 | 401 | 403 | 422h | 404 | 422h | 422h | 422h | 403 | 401 |
| `PUT /v1/properties/:id/water [rate change]` | 401 | 401 | 403 | 403 | 404 | 2xx | 2xx | 2xx | 403 | 401 |
| `PUT /v1/properties/:id/water [rate echo]` | 401 | 401 | 403 | 2xx | 404 | 2xx | 2xx | 2xx | 403 | 401 |
| `PUT /v1/properties/:id/water [reading only]` | 401 | 401 | 403 | 2xx | 404 | 2xx | 2xx | 2xx | 403 | 401 |
| **q** | | | | | | | | | | |
| `GET /q/:token` | 302h | 302h | 302h | 302h | 302h | 302h | 302h | 302h | 302h | 302h |
| **units** | | | | | | | | | | |
| `GET /v1/units/:id` | 401 | 401 | 2xx | 2xx | 404 | 2xx | 2xx | 2xx | 403 | 401 |
| `GET /v1/units/:id/leases` | 401 | 401 | 2xx | 2xx | 404 | 2xx | 2xx | 2xx | 403 | 401 |
| `GET /v1/units/:id/ledger/:type` | 401 | 401 | 2xx | 2xx | 404 | 2xx | 2xx | 2xx | 403 | 401 |
| `PATCH /v1/units/:id` | 401 | 401 | 403 | 2xx | 404 | 2xx | 2xx | 2xx | 403 | 401 |
| `PATCH /v1/units/:id [label only]` | 401 | 401 | 403 | 2xx | 404 | 2xx | 2xx | 2xx | 403 | 401 |
| `PATCH /v1/units/:id [rent change]` | 401 | 401 | 403 | 403 | 404 | 422h | 422h | 422h | 403 | 401 |
| `PATCH /v1/units/:id [rent echo]` | 401 | 401 | 403 | 2xx | 404 | 2xx | 2xx | 2xx | 403 | 401 |
| `POST /v1/units/:id/arrears-commitments` | 401 | 401 | 403 | 403 | 404 | 422h | 422h | 422h | 403 | 401 |
| `POST /v1/units/:id/leases` | 401 | 401 | 403 | 403 | 404 | 422h | 422h | 422h | 403 | 401 |
| `POST /v1/units/:id/rent/charges` | 401 | 401 | 403 | 403 | 404 | 422h | 422h | 422h | 403 | 401 |
| `POST /v1/units/:id/rent/charges [late fee]` | 401 | 401 | 403 | 403 | 404 | 2xx | 2xx | 2xx | 403 | 401 |
| `POST /v1/units/:id/rent/payments` | 401 | 401 | 403 | 422h | 404 | 422h | 422h | 422h | 403 | 401 |
| `POST /v1/units/:id/rent/payments [cash payment]` | 401 | 401 | 403 | 2xx | 404 | 2xx | 2xx | 2xx | 403 | 401 |
| **webhooks** | | | | | | | | | | |
| `POST /v1/webhooks/payhero/collections` | 401 | 401 | 401 | 401 | 401 | 401 | 401 | 401 | 401 | 401 |
