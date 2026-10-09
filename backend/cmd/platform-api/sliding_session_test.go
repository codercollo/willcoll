package main

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/codercollo/willcoll/backend/internal/shared/auth"
)

// Sliding sessions (build-phases.txt, "Sliding sessions"): activity renews a
// session, at most once per idle/4; idleness expires it; the absolute cap
// always wins; and a suspended firm is cut off whatever renewal is doing.

const (
	testIdle = 2 * time.Hour
	testCap  = 12 * time.Hour
)

type testClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *testClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *testClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func newSlidingFixture(t *testing.T) (*fakePlatform, *testClock) {
	t.Helper()
	f := newFakePlatform(t)
	clock := &testClock{t: time.Date(2026, 3, 2, 8, 0, 0, 0, time.UTC)}
	f.app.sessionPolicy = auth.SlidingPolicy{Idle: testIdle, MaxLifetime: testCap, Now: clock.now}
	return f, clock
}

// touchedAt reads the session row's last renewal straight from the store.
func (f *fakePlatform) touchedAt(t *testing.T, c *http.Cookie) time.Time {
	t.Helper()
	sm := f.app.sessionManager
	ctx, err := sm.Load(context.Background(), c.Value)
	if err != nil {
		t.Fatal(err)
	}
	return auth.SessionTime(sm, ctx, auth.SessionKeyTouchedAt)
}

func (f *fakePlatform) whoami(c *http.Cookie) int {
	return f.do(http.MethodGet, "/v1/sessions", "", c).Code
}

func TestActivityKeepsASessionAlivePastTheIdleTimeout(t *testing.T) {
	f, clock := newSlidingFixture(t)
	cookie, code := f.login(t, fakeManagerEmail)
	if code != http.StatusOK {
		t.Fatalf("login: %d", code)
	}

	// One request every 90 minutes for 9 hours: far more than the 2h idle
	// window in total, never more than it between requests.
	for i := 1; i <= 6; i++ {
		clock.advance(90 * time.Minute)
		if got := f.whoami(cookie); got != http.StatusOK {
			t.Fatalf("request %d at +%v: %d, want 200", i, time.Duration(i)*90*time.Minute, got)
		}
	}
}

func TestIdleSessionExpires(t *testing.T) {
	f, clock := newSlidingFixture(t)
	for name, email := range map[string]string{"manager": fakeManagerEmail, "staff": fakeStaffEmail} {
		cookie, _ := f.login(t, email)
		clock.advance(testIdle - time.Minute)
		if got := f.whoami(cookie); got != http.StatusOK {
			t.Fatalf("%s just inside the idle window: %d, want 200", name, got)
		}
		clock.advance(testIdle) // idle for the whole window since the renewal
		if got := f.whoami(cookie); got != http.StatusUnauthorized {
			t.Errorf("%s idle past the timeout: %d, want 401", name, got)
		}
		// The row is deleted, not merely ignored: it stays dead.
		if got := f.whoami(cookie); got != http.StatusUnauthorized {
			t.Errorf("%s idle session, second try: %d, want 401", name, got)
		}
	}
}

func TestAbsoluteCapAlwaysExpiresASession(t *testing.T) {
	for _, who := range []string{"manager", "staff", "admin"} {
		t.Run(who, func(t *testing.T) {
			f, clock := newSlidingFixture(t)
			var cookie *http.Cookie
			switch who {
			case "manager":
				cookie, _ = f.login(t, fakeManagerEmail)
			case "staff":
				cookie, _ = f.login(t, fakeStaffEmail)
			default:
				cookie = f.stampedSession(t, principalAdmin, fakeAdminID)
			}

			// Constantly active: a request every hour, right up to the cap.
			for h := 1; h < 12; h++ {
				clock.advance(time.Hour)
				if got := f.whoami(cookie); got != http.StatusOK {
					t.Fatalf("hour %d: %d, want 200", h, got)
				}
			}
			clock.advance(time.Hour) // 12h after login, with a request 1h ago
			if got := f.whoami(cookie); got != http.StatusUnauthorized {
				t.Errorf("at the cap: %d, want 401 however active the user was", got)
			}
		})
	}
}

func TestRenewalIsThrottledToOncePerQuarterOfIdle(t *testing.T) {
	f, clock := newSlidingFixture(t)
	cookie, _ := f.login(t, fakeManagerEmail)
	loggedIn := f.touchedAt(t, cookie)

	clock.advance(testIdle/4 - time.Minute)
	f.whoami(cookie)
	if got := f.touchedAt(t, cookie); !got.Equal(loggedIn) {
		t.Errorf("renewed after only idle/4 - 1m: touched_at %v, want unchanged %v", got, loggedIn)
	}

	clock.advance(time.Minute)
	f.whoami(cookie)
	if got := f.touchedAt(t, cookie); !got.Equal(clock.now()) {
		t.Errorf("not renewed at idle/4: touched_at %v, want %v", got, clock.now())
	}
}

func TestRenewalNeverMovesTheCap(t *testing.T) {
	f, clock := newSlidingFixture(t)
	cookie, _ := f.login(t, fakeManagerEmail)
	sm := f.app.sessionManager
	created := func() time.Time {
		ctx, err := sm.Load(context.Background(), cookie.Value)
		if err != nil {
			t.Fatal(err)
		}
		return auth.SessionTime(sm, ctx, auth.SessionKeyCreatedAt)
	}
	at := created()
	for i := 0; i < 5; i++ {
		clock.advance(time.Hour)
		f.whoami(cookie)
	}
	if got := created(); !got.Equal(at) {
		t.Errorf("created_at moved from %v to %v across renewals", at, got)
	}
}

func TestSuspensionStillCutsOffInstantlyWithRenewal(t *testing.T) {
	f, clock := newSlidingFixture(t)
	admin := f.stampedSession(t, principalAdmin, fakeAdminID)
	f.stepUp(t, admin)
	manager, _ := f.login(t, fakeManagerEmail)
	staff, _ := f.login(t, fakeStaffEmail)

	// Active, renewed sessions.
	clock.advance(time.Hour)
	for name, c := range map[string]*http.Cookie{"manager": manager, "staff": staff, "admin": admin} {
		if got := f.whoami(c); got != http.StatusOK {
			t.Fatalf("%s before suspension: %d", name, got)
		}
	}

	suspend := "/v1/admin/managers/" + fakeManagerID.String() + "/suspend"
	if w := f.do(http.MethodPost, suspend, "", admin); w.Code != http.StatusOK {
		t.Fatalf("suspend: %d %s", w.Code, w.Body)
	}
	for name, c := range map[string]*http.Cookie{"manager": manager, "staff": staff} {
		if got := f.whoami(c); got != http.StatusUnauthorized {
			t.Errorf("%s right after suspension: %d, want 401", name, got)
		}
	}
	if got := f.whoami(admin); got != http.StatusOK {
		t.Errorf("the admin was cut off too: %d", got)
	}

	// A session that survives (minted behind our back) is not extended by
	// activity: its touched_at stays put and it is refused on status.
	ghost := f.stampedSession(t, principalManager, fakeManagerID)
	before := f.touchedAt(t, ghost)
	clock.advance(time.Hour)
	if w := f.do(http.MethodGet, "/v1/account", "", ghost); w.Code != http.StatusForbidden {
		t.Errorf("suspended manager with a surviving session: %d, want 403", w.Code)
	}
	if got := f.touchedAt(t, ghost); !got.Equal(before) {
		t.Errorf("a suspended manager's session was renewed: %v -> %v", before, got)
	}
}

// stampedSession is f.session plus the timestamps login writes, at the
// fixture clock's current time.
func (f *fakePlatform) stampedSession(t *testing.T, principalType string, id interface{ String() string }) *http.Cookie {
	t.Helper()
	sm := f.app.sessionManager
	ctx, err := sm.Load(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	sm.Put(ctx, sessionKeyPrincipalType, principalType)
	sm.Put(ctx, sessionKeyPrincipalID, id.String())
	f.app.sessionPolicy.Stamp(sm, ctx)
	token, _, err := sm.Commit(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return &http.Cookie{Name: fakeSessionCookie, Value: token}
}
