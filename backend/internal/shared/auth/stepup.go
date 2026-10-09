package auth

import "time"

// Step-up authentication (docs/security/authorization.md, D5 resolution).
//
// A few actions are dangerous enough that a live session alone is not enough:
// changing a payment channel, reversing a ledger entry, and changing or
// removing a staff member's authority. For these the caller re-enters their
// own password, which opens a short window (StepUpTTL) in which the gated
// actions are allowed.
//
// The window is one value in the shared platform session
// (SessionKeyStepUpUntil, unix seconds). Only platform-api writes sessions
// (ADR-0007), so only it can open a window (POST /v1/sessions/step-up); every
// product API reads the value read-only and decides with StepUpActive. The
// window dies with the session: sign-out, revocation and suspension all end it.

// StepUpTTL is how long a successful password re-entry stays valid.
const StepUpTTL = 5 * time.Minute

// StepUpPermissions are the catalog permissions whose routes require a fresh
// window (C3.6): where money goes (payment channels), reversing ledger
// entries, and who holds authority (staff management; the staff route gates
// only authority changes, not a rename). A test fails when a catalog
// permission ending in ".reverse" (e.g. a future payments.reverse) is missing
// from this list, and each product API's route test fails when a route
// guarded by one of them has no gate.
var StepUpPermissions = []string{PaymentChannelsManage, ChargesReverse, StaffManage}

// StepUpMaxFailures is how many wrong re-entries a session may make before it
// is ended: someone holding a stolen session cookie must not be able to guess
// the password through the step-up endpoint.
const StepUpMaxFailures = 5

// Session keys. The context key carries the session's value onto the request
// (set by each binary's authenticate), so handlers and tests read one place.
const (
	SessionKeyStepUpUntil    = "stepup_until"
	SessionKeyStepUpFailures = "stepup_failures"
	StepUpContextKey         = "stepUpUntil"
)

// StepUpCode is the machine-readable error code a client branches on to show
// the password prompt and retry.
const StepUpCode = "step_up_required"

// StepUpUntil returns the expiry (unix seconds) of a window opened at now.
func StepUpUntil(now time.Time) int64 {
	return now.Add(StepUpTTL).Unix()
}

// StepUpActive reports whether a window expiring at untilUnix is still open
// at now. Zero (never stepped up) is closed.
func StepUpActive(untilUnix int64, now time.Time) bool {
	return untilUnix > 0 && now.Unix() < untilUnix
}
