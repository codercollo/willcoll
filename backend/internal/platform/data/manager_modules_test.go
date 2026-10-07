package data

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/codercollo/willcoll/backend/internal/platform/db/mock"
	"github.com/codercollo/willcoll/backend/internal/platform/db/sqlc"
	"github.com/google/uuid"
	"go.uber.org/mock/gomock"
)

func newTestModuleModel(t *testing.T) (ModuleModel, *mock.MockQuerier) {
	t.Helper()
	ctrl := gomock.NewController(t)
	store := mock.NewMockStore(ctrl)
	q := mock.NewMockQuerier(ctrl)
	store.EXPECT().ExecTenantTx(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ uuid.UUID, fn func(sqlc.Querier) error) error { return fn(q) }).
		AnyTimes()
	return ModuleModel{Store: store}, q
}

func TestModuleModel_StartTrial_NewModuleGetsFreshTrial(t *testing.T) {
	m, q := newTestModuleModel(t)
	tenantID := uuid.New()

	q.EXPECT().StartModuleTrial(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ any, a sqlc.StartModuleTrialParams) (sqlc.ManagerModule, error) {
			if a.Product != ProductEstates {
				t.Errorf("product = %q, want estates", a.Product)
			}
			return sqlc.ManagerModule{ManagerID: tenantID, Product: a.Product, Status: ModuleStatusTrialing, TrialEndsAt: a.TrialEndsAt}, nil
		})

	got, err := m.StartTrial(context.Background(), tenantID, ProductEstates, 7)
	if err != nil {
		t.Fatalf("StartTrial: %v", err)
	}
	if got.Status != ModuleStatusTrialing || got.TrialEndsAt == nil {
		t.Errorf("got = %+v, want trialing with a trial_ends_at", got)
	}
}

// TestModuleModel_StartTrial_IndependentPerProduct pins down build-phases.txt
// 3f-3's headline test: a manager whose Rentals trial has already expired
// still gets a fresh, independent Estates trial when they start it — the
// trial belongs to the module row, not the manager.
func TestModuleModel_StartTrial_IndependentPerProduct(t *testing.T) {
	m, q := newTestModuleModel(t)
	tenantID := uuid.New()
	expiredRentalsTrial := time.Now().AddDate(0, 0, -30)

	// Rentals' trial already ran out — StartTrial for estates must not
	// look at it, or be blocked by it.
	q.EXPECT().StartModuleTrial(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ any, a sqlc.StartModuleTrialParams) (sqlc.ManagerModule, error) {
			return sqlc.ManagerModule{ManagerID: tenantID, Product: ProductEstates, Status: ModuleStatusTrialing, TrialEndsAt: a.TrialEndsAt}, nil
		})

	got, err := m.StartTrial(context.Background(), tenantID, ProductEstates, DefaultTrialDays)
	if err != nil {
		t.Fatalf("StartTrial: %v", err)
	}
	if got.TrialEndsAt == nil || !got.TrialEndsAt.After(expiredRentalsTrial) {
		t.Fatalf("estates trial_ends_at = %v, want a fresh future date independent of the expired rentals trial", got.TrialEndsAt)
	}
	if !got.TrialEndsAt.After(time.Now()) {
		t.Errorf("estates trial_ends_at = %v, want in the future", got.TrialEndsAt)
	}
}

// TestModuleModel_StartTrial_AlreadyStartedNeverExtends: a retried or
// duplicate call must never push the trial's end date out further — it
// reads back the existing row instead.
func TestModuleModel_StartTrial_AlreadyStartedNeverExtends(t *testing.T) {
	m, q := newTestModuleModel(t)
	tenantID := uuid.New()
	originalEnd := time.Now().AddDate(0, 0, 3)

	q.EXPECT().StartModuleTrial(gomock.Any(), gomock.Any()).Return(sqlc.ManagerModule{}, sql.ErrNoRows)
	q.EXPECT().GetManagerModule(gomock.Any(), gomock.Any()).Return(
		sqlc.ManagerModule{ManagerID: tenantID, Product: ProductRentals, Status: ModuleStatusActive, TrialEndsAt: &originalEnd}, nil)

	got, err := m.StartTrial(context.Background(), tenantID, ProductRentals, DefaultTrialDays)
	if err != nil {
		t.Fatalf("StartTrial: %v", err)
	}
	if got.TrialEndsAt == nil || !got.TrialEndsAt.Equal(originalEnd) {
		t.Errorf("trial_ends_at = %v, want the original %v unchanged", got.TrialEndsAt, originalEnd)
	}
	if got.Status != ModuleStatusActive {
		t.Errorf("status = %q, want the existing row's status left alone", got.Status)
	}
}
