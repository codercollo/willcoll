package auth

import (
	"context"
	"time"

	"github.com/alexedwards/scs/v2"
)

// Sliding sessions (build-phases.txt, "Sliding sessions").
//
// SCS's own IdleTimeout re-commits the session row on EVERY request (Load
// marks it Modified), which is a database write per request, and a reader
// binary such as rentals-api could never take part in it without committing.
// So the shared session manager runs with SCS IdleTimeout = 0 and the policy
// below is enforced explicitly, from two timestamps stored in the session:
//
//   - created_at: stamped once at login. created_at + MaxLifetime is the
//     absolute cap; renewal never moves it, so it holds across any number of
//     touches. (SCS's own Lifetime, set to the same value, is a second,
//     independent backstop: it bounds the store row.)
//   - touched_at: the last renewal. A session whose touched_at is older than
//     Idle is expired. Only platform-api writes it, and at most once every
//     Idle/4 (Touch).
//
// Both platform-api (which renews) and the product APIs (which only read, and
// never Commit) call Expired, so an idle or over-age session is anonymous
// everywhere.
const (
	SessionKeyCreatedAt = "created_at"
	SessionKeyTouchedAt = "touched_at"
)

// SessionTime reads one of the timestamps above, zero if absent. They are
// stored as Unix seconds (int64): SCS's gob codec can only encode a time.Time
// held in the session after gob.Register, and a plain int needs nothing.
func SessionTime(sm *scs.SessionManager, ctx context.Context, key string) time.Time {
	if sec := sm.GetInt64(ctx, key); sec != 0 {
		return time.Unix(sec, 0)
	}
	return time.Time{}
}

// SlidingPolicy is the idle and absolute limits. The zero value disables
// both checks (used by tests that don't care about sessions).
type SlidingPolicy struct {
	// Idle is how long a session survives without a touch.
	Idle time.Duration
	// MaxLifetime is the absolute cap from login, however active the user is.
	MaxLifetime time.Duration
	// Now is the clock; nil means time.Now. Injectable for tests.
	Now func() time.Time
}

func (p SlidingPolicy) now() time.Time {
	if p.Now != nil {
		return p.Now()
	}
	return time.Now()
}

// RenewEvery is the minimum gap between two touches of one session.
func (p SlidingPolicy) RenewEvery() time.Duration { return p.Idle / 4 }

// Stamp records a fresh session's creation and first touch. Call it at login
// (after RenewToken).
func (p SlidingPolicy) Stamp(sm *scs.SessionManager, ctx context.Context) {
	now := p.now().UTC()
	sm.Put(ctx, SessionKeyCreatedAt, now.Unix())
	sm.Put(ctx, SessionKeyTouchedAt, now.Unix())
}

// Expired reports whether the loaded session is past its absolute cap or has
// been idle too long. A session carrying no timestamps (issued before this
// policy existed) is not expired here: SCS's own deadline still bounds it,
// and the next touch stamps it.
func (p SlidingPolicy) Expired(sm *scs.SessionManager, ctx context.Context) bool {
	now := p.now()
	if p.MaxLifetime > 0 {
		if created := SessionTime(sm, ctx, SessionKeyCreatedAt); !created.IsZero() && now.Sub(created) >= p.MaxLifetime {
			return true
		}
	}
	if p.Idle > 0 {
		if touched := SessionTime(sm, ctx, SessionKeyTouchedAt); !touched.IsZero() && now.Sub(touched) >= p.Idle {
			return true
		}
	}
	return false
}

// Touch renews the session if its last touch is at least RenewEvery old (or
// it has none), reporting whether it wrote. A renewal Puts touched_at, which
// marks the session Modified so the caller's usual commit persists it; an
// untouched request stays Unmodified and costs no write. Callers must have
// checked Expired first and must only touch a session whose principal is
// still allowed in.
func (p SlidingPolicy) Touch(sm *scs.SessionManager, ctx context.Context) bool {
	if p.Idle <= 0 {
		return false
	}
	now := p.now()
	if touched := SessionTime(sm, ctx, SessionKeyTouchedAt); !touched.IsZero() && now.Sub(touched) < p.RenewEvery() {
		return false
	}
	sm.Put(ctx, SessionKeyTouchedAt, now.Unix())
	if SessionTime(sm, ctx, SessionKeyCreatedAt).IsZero() {
		// A pre-policy session: its age is unknown, so the cap starts now.
		sm.Put(ctx, SessionKeyCreatedAt, now.Unix())
	}
	return true
}
