package data

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/codercollo/willcoll/backend/internal/platform/db/mock"
	"github.com/codercollo/willcoll/backend/internal/platform/db/sqlc"
	"github.com/google/uuid"
	"go.uber.org/mock/gomock"
)

func TestBillingModel_DevOnlyPaths_RefuseOutsideDevelopment(t *testing.T) {
	// ExpireForDev, SetPlanForDev and DeleteForDev check ENV themselves
	// (unlike rentals-api's original versions, gated only by cmd/devtools'
	// own process-level check) — nothing in platform-api must be able to
	// run them against staging/production, even if a future devtools-for-
	// platform tool forgets its own outer guard.
	ctx := context.Background()
	for _, env := range []string{"", "staging", "production"} {
		m := BillingModel{Env: env}

		if _, err := m.ExpireForDev(ctx, uuid.New(), ProductRentals, time.Now()); !errors.Is(err, ErrNotDevelopment) {
			t.Errorf("ExpireForDev env=%q: err = %v, want ErrNotDevelopment", env, err)
		}
		if err := m.SetPlanForDev(ctx, uuid.New(), ProductRentals, uuid.New()); !errors.Is(err, ErrNotDevelopment) {
			t.Errorf("SetPlanForDev env=%q: err = %v, want ErrNotDevelopment", env, err)
		}
		if _, err := m.DeleteForDev(ctx, uuid.New(), ProductRentals); !errors.Is(err, ErrNotDevelopment) {
			t.Errorf("DeleteForDev env=%q: err = %v, want ErrNotDevelopment", env, err)
		}
	}
}

func TestBillingModel_StartRenewal_RefusesTestPlanOutsideDevelopment(t *testing.T) {
	ctrl := gomock.NewController(t)
	store := mock.NewMockStore(ctrl)
	q := mock.NewMockQuerier(ctrl)
	store.EXPECT().ExecTenantTx(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ uuid.UUID, fn func(sqlc.Querier) error) error { return fn(q) }).
		AnyTimes()

	tenantID := uuid.New()
	planID := uuid.New()

	q.EXPECT().GetModuleUsage(gomock.Any(), gomock.Any()).Return(sqlc.ModuleUsage{}, sql.ErrNoRows)
	q.EXPECT().GetSubscriptionByManagerAndProduct(gomock.Any(), gomock.Any()).Return(sqlc.Subscription{}, sql.ErrNoRows)
	q.EXPECT().GetSubscriptionPlan(gomock.Any(), planID).Return(sqlc.SubscriptionPlan{
		ID: planID, Product: ProductRentals, Name: "TEST - KES 10", IsTest: true,
		Price: money(t, "10.00"), BillingInterval: "monthly", PricingType: PricingFlat,
	}, nil)

	m := BillingModel{Store: store, Usage: UsageModel{Store: store}, Env: "staging"}
	_, err := m.StartRenewal(context.Background(), tenantID, ProductRentals, &planID)
	if !errors.Is(err, ErrPlanNotAvailable) {
		t.Fatalf("err = %v, want ErrPlanNotAvailable", err)
	}
}

func TestBillingModel_StartRenewal_AllowsTestPlanInDevelopment(t *testing.T) {
	ctrl := gomock.NewController(t)
	store := mock.NewMockStore(ctrl)
	q := mock.NewMockQuerier(ctrl)
	store.EXPECT().ExecTenantTx(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ uuid.UUID, fn func(sqlc.Querier) error) error { return fn(q) }).
		AnyTimes()

	tenantID := uuid.New()
	planID := uuid.New()
	subID := uuid.New()

	q.EXPECT().GetModuleUsage(gomock.Any(), gomock.Any()).Return(sqlc.ModuleUsage{}, sql.ErrNoRows)
	q.EXPECT().GetSubscriptionByManagerAndProduct(gomock.Any(), gomock.Any()).Return(sqlc.Subscription{}, sql.ErrNoRows)
	q.EXPECT().GetSubscriptionPlan(gomock.Any(), planID).Return(sqlc.SubscriptionPlan{
		ID: planID, Product: ProductRentals, Name: "TEST - KES 10", IsTest: true,
		Price: money(t, "10.00"), BillingInterval: "monthly", PricingType: PricingFlat,
	}, nil)
	q.EXPECT().CreateSubscription(gomock.Any(), gomock.Any()).Return(sqlc.Subscription{
		ID: subID, ManagerID: tenantID, Product: ProductRentals, PlanID: planID, Status: SubscriptionPastDue,
	}, nil)
	q.EXPECT().CreateSubscriptionInvoice(gomock.Any(), gomock.Any()).Return(sqlc.SubscriptionInvoice{
		ID: uuid.New(), ManagerID: tenantID, SubscriptionID: subID, PlanID: planID, Amount: money(t, "10.00"),
		MpesaReceipt: "PENDING-x", Status: InvoicePending,
	}, nil)
	q.EXPECT().GetManager(gomock.Any(), tenantID).Return(sqlc.Manager{ID: tenantID, FirmName: "Acme", Phone: "+254700000000"}, nil)

	m := BillingModel{Store: store, Usage: UsageModel{Store: store}, Env: "development"}
	renewal, err := m.StartRenewal(context.Background(), tenantID, ProductRentals, &planID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if renewal.Plan.Name != "TEST - KES 10" {
		t.Errorf("Plan.Name = %q, want the TEST plan", renewal.Plan.Name)
	}
}
