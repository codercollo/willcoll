package main

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/codercollo/willcoll/backend/internal/shared/auth"
	"go.uber.org/mock/gomock"
)

// rentals-api only CHECKS platform-api's sliding-session limits (it never
// renews): an idle or over-age session is anonymous here, a fresh one is not.
func TestPlatformSessionPolicyExpiresIdleAndOverAgeSessions(t *testing.T) {
	const target = "/v1/properties/not-a-uuid" // 404 from the handler = past every guard
	now := time.Date(2026, 3, 2, 12, 0, 0, 0, time.UTC)

	for _, c := range []struct {
		name            string
		created, touche time.Time
		want            int
	}{
		{"fresh", now.Add(-time.Hour), now.Add(-10 * time.Minute), http.StatusNotFound},
		{"idle past the timeout", now.Add(-5 * time.Hour), now.Add(-2*time.Hour - time.Second), http.StatusUnauthorized},
		{"past the absolute cap despite recent activity", now.Add(-12 * time.Hour), now.Add(-time.Minute), http.StatusUnauthorized},
	} {
		t.Run(c.name, func(t *testing.T) {
			m := newMiddlewareTestApp(t)
			m.app.platformSessionPolicy = auth.SlidingPolicy{
				Idle: 2 * time.Hour, MaxLifetime: 12 * time.Hour, Now: func() time.Time { return now },
			}
			if c.want == http.StatusNotFound {
				m.pStore.EXPECT().GetManager(gomock.Any(), testManagerID).Return(managerRow("active"), nil)
			}

			sm := m.app.platformSessionManager
			ctx, err := sm.Load(context.Background(), "")
			if err != nil {
				t.Fatal(err)
			}
			sm.Put(ctx, sessionKeyPrincipalType, principalManager)
			sm.Put(ctx, sessionKeyPrincipalID, testManagerID.String())
			sm.Put(ctx, auth.SessionKeyCreatedAt, c.created.Unix())
			sm.Put(ctx, auth.SessionKeyTouchedAt, c.touche.Unix())
			token, _, err := sm.Commit(ctx)
			if err != nil {
				t.Fatal(err)
			}

			w := m.do(http.MethodGet, target, &http.Cookie{Name: sm.Cookie.Name, Value: token}, nil)
			if w.Code != c.want {
				t.Errorf("status = %d, want %d (%s)", w.Code, c.want, w.Body)
			}
		})
	}
}
