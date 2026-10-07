package auth

import (
	"context"
	"testing"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/alexedwards/scs/v2/memstore"
)

func TestSlidingPolicy(t *testing.T) {
	now := time.Date(2026, 3, 2, 8, 0, 0, 0, time.UTC)
	p := SlidingPolicy{Idle: 2 * time.Hour, MaxLifetime: 12 * time.Hour, Now: func() time.Time { return now }}

	sm := scs.New()
	sm.Store = memstore.New()
	ctx, err := sm.Load(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}

	// No timestamps (a pre-policy session): not expired, and a touch stamps it.
	if p.Expired(sm, ctx) {
		t.Error("a session without timestamps must not be expired by the policy")
	}
	if !p.Touch(sm, ctx) {
		t.Error("first touch should write")
	}
	created := SessionTime(sm, ctx, SessionKeyCreatedAt)

	// Within idle/4: no write.
	now = now.Add(p.RenewEvery() - time.Second)
	if p.Touch(sm, ctx) {
		t.Error("touch inside idle/4 must not write")
	}
	now = now.Add(time.Second)
	if !p.Touch(sm, ctx) {
		t.Error("touch at idle/4 must write")
	}
	if got := SessionTime(sm, ctx, SessionKeyCreatedAt); !got.Equal(created) {
		t.Errorf("created_at moved: %v -> %v", created, got)
	}

	// Idle boundary.
	now = now.Add(2*time.Hour - time.Second)
	if p.Expired(sm, ctx) {
		t.Error("expired one second early")
	}
	now = now.Add(time.Second)
	if !p.Expired(sm, ctx) {
		t.Error("not expired at the idle timeout")
	}

	// Cap boundary, however recent the touch.
	now = created.Add(12*time.Hour - time.Second)
	sm.Put(ctx, SessionKeyTouchedAt, now.Unix())
	if p.Expired(sm, ctx) {
		t.Error("expired before the cap")
	}
	now = created.Add(12 * time.Hour)
	sm.Put(ctx, SessionKeyTouchedAt, now.Unix())
	if !p.Expired(sm, ctx) {
		t.Error("not expired at the absolute cap")
	}

	// What Touch wrote must survive the store's real codec.
	if _, _, err := sm.Commit(ctx); err != nil {
		t.Errorf("committing a stamped session: %v", err)
	}

	// The zero policy disables everything.
	var zero SlidingPolicy
	if zero.Expired(sm, ctx) || zero.Touch(sm, ctx) {
		t.Error("zero policy must be inert")
	}
}
