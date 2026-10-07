package main

import (
	"context"
	"database/sql"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/alexedwards/scs/v2/memstore"
	platformdata "github.com/codercollo/willcoll/backend/internal/platform/data"
	platformmock "github.com/codercollo/willcoll/backend/internal/platform/db/mock"
	platformsqlc "github.com/codercollo/willcoll/backend/internal/platform/db/sqlc"
	"github.com/google/uuid"
	"go.uber.org/mock/gomock"
)

// platformStub stands in for willcoll_platform in the integration tests that
// run rentals-api against a real Rentals database (build-phases.txt 3i-b):
// rentals-api has no identity tables of its own any more, so a firm's
// manager row, Rentals module, trial and subscription — and the session
// platform-api would have issued at login — all come from here, in memory.
// Tests change a firm's platform state by editing its firm struct, the way
// platform-api's endpoints (activation, a paid renewal, a suspension)
// would change the real rows; rentals-api reads the result on every
// request.
type platformStub struct {
	models  *platformdata.Models
	session *scs.SessionManager

	mu    sync.Mutex
	firms map[uuid.UUID]*stubFirm
}

// stubFirm is one manager's willcoll_platform state.
type stubFirm struct {
	Status      string
	ModuleState string // manager_modules.status; "" = no row
	TrialEndsAt *time.Time
	// Subscription is the firm's rentals subscription; nil = none yet.
	Subscription *platformsqlc.Subscription
}

func newPlatformStub(t *testing.T) *platformStub {
	t.Helper()
	ctrl := gomock.NewController(t)
	pStore := platformmock.NewMockStore(ctrl)
	pq := platformmock.NewMockQuerier(ctrl)
	pStore.EXPECT().ExecTenantTx(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ uuid.UUID, fn func(platformsqlc.Querier) error) error { return fn(pq) }).
		AnyTimes()
	pq.EXPECT().CreateAuditLog(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	// The usage reporter (usage.go) fires after unit changes; accept it.
	pq.EXPECT().UpsertModuleUsage(gomock.Any(), gomock.Any()).Return(platformsqlc.ModuleUsage{}, nil).AnyTimes()

	s := &platformStub{firms: map[uuid.UUID]*stubFirm{}}

	pStore.EXPECT().GetManager(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, id uuid.UUID) (platformsqlc.Manager, error) {
		s.mu.Lock()
		defer s.mu.Unlock()
		f, ok := s.firms[id]
		if !ok {
			return platformsqlc.Manager{}, sql.ErrNoRows
		}
		now := time.Now()
		return platformsqlc.Manager{ID: id, FirmName: "Test Firm", Username: "u" + id.String()[:8],
			Email: id.String()[:8] + "@stub.example.com", Phone: "+254700000000", Status: f.Status, ActivatedAt: &now, Version: 1}, nil
	}).AnyTimes()

	pq.EXPECT().GetManagerModule(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, arg platformsqlc.GetManagerModuleParams) (platformsqlc.ManagerModule, error) {
		s.mu.Lock()
		defer s.mu.Unlock()
		f, ok := s.firms[arg.ManagerID]
		if !ok || f.ModuleState == "" {
			return platformsqlc.ManagerModule{}, sql.ErrNoRows
		}
		return platformsqlc.ManagerModule{ManagerID: arg.ManagerID, Product: arg.Product, Status: f.ModuleState, TrialEndsAt: f.TrialEndsAt}, nil
	}).AnyTimes()

	pq.EXPECT().GetSubscriptionByManagerAndProduct(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, arg platformsqlc.GetSubscriptionByManagerAndProductParams) (platformsqlc.Subscription, error) {
		s.mu.Lock()
		defer s.mu.Unlock()
		f, ok := s.firms[arg.ManagerID]
		if !ok || f.Subscription == nil {
			return platformsqlc.Subscription{}, sql.ErrNoRows
		}
		return *f.Subscription, nil
	}).AnyTimes()

	platformModels := platformdata.NewModelsFromStore(pStore, 0)
	s.models = &platformModels

	s.session = scs.New()
	s.session.Store = memstore.New()
	s.session.Cookie.Name = platformSessionCookieName
	return s
}

// attach points app at the stub the way main() wires the real platform.
func (s *platformStub) attach(app *application) {
	app.platform = s.models
	app.platformSessionManager = s.session
}

// newFirm registers an activated manager with an active Rentals module (no
// trial, no subscription) and returns its id, which is also its tenant id.
func (s *platformStub) newFirm() uuid.UUID {
	id := uuid.New()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.firms[id] = &stubFirm{Status: platformdata.ManagerStatusActive, ModuleState: platformdata.ModuleStatusActive}
	return id
}

// edit changes a firm's platform state under the stub's lock.
func (s *platformStub) edit(id uuid.UUID, fn func(f *stubFirm)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fn(s.firms[id])
}

// login returns the Cookie header a browser would send after platform-api
// signed principalID in as principalType.
func (s *platformStub) login(t *testing.T, principalType string, principalID uuid.UUID) string {
	t.Helper()
	ctx, err := s.session.Load(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	s.session.Put(ctx, sessionKeyPrincipalType, principalType)
	s.session.Put(ctx, sessionKeyPrincipalID, principalID.String())
	token, _, err := s.session.Commit(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return (&http.Cookie{Name: platformSessionCookieName, Value: token}).String()
}

// loginManager is login for a manager principal.
func (s *platformStub) loginManager(t *testing.T, id uuid.UUID) string {
	return s.login(t, principalManager, id)
}
