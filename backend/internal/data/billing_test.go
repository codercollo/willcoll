package data

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/codercollo/willcoll/backend/internal/db/mock"
	"github.com/codercollo/willcoll/backend/internal/db/sqlc"
	"github.com/codercollo/willcoll/backend/internal/payhero"
	"github.com/codercollo/willcoll/backend/pkg/moneyfmt"
	"github.com/google/uuid"
	"go.uber.org/mock/gomock"
)

// newTestBillingModel wires a BillingModel to a mocked store, running every
// tenant transaction against q — the pattern cmd/api's payTestApp uses, kept
// local here since these guards belong to the service layer.
func newTestBillingModel(t *testing.T, env string) (BillingModel, *mock.MockQuerier, *mock.MockStore) {
	t.Helper()
	ctrl := gomock.NewController(t)
	store := mock.NewMockStore(ctrl)
	q := mock.NewMockQuerier(ctrl)
	store.EXPECT().ExecTenantTx(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ uuid.UUID, fn func(sqlc.Querier) error) error { return fn(q) }).
		AnyTimes()
	return BillingModel{Store: store, Resolver: ResolverModel{Store: store}, Env: env}, q, store
}

func moneyKES(s string) moneyfmt.Money {
	m, err := moneyfmt.Parse(s)
	if err != nil {
		panic(err)
	}
	return m
}

func TestStartRenewalTestPlanGuard(t *testing.T) {
	planID := uuid.New()
	testPlan := sqlc.SubscriptionPlan{ID: planID, Name: "TEST - KES 10", Price: moneyKES("10.00"), BillingInterval: "monthly", IsTest: true}

	for _, env := range []string{"staging", "production", ""} {
		t.Run("refused outside development ("+env+")", func(t *testing.T) {
			m, q, _ := newTestBillingModel(t, env)
			q.EXPECT().GetSubscriptionByManager(gomock.Any(), gomock.Any()).Return(sqlc.Subscription{}, sql.ErrNoRows)
			q.EXPECT().GetSubscriptionPlan(gomock.Any(), planID).Return(testPlan, nil)

			_, err := m.StartRenewal(context.Background(), uuid.New(), &planID)
			if !errors.Is(err, ErrPlanNotAvailable) {
				t.Fatalf("err = %v, want ErrPlanNotAvailable", err)
			}
		})
	}

	t.Run("allowed in development", func(t *testing.T) {
		m, q, _ := newTestBillingModel(t, "development")
		tenantID := uuid.New()
		q.EXPECT().GetSubscriptionByManager(gomock.Any(), gomock.Any()).Return(sqlc.Subscription{}, sql.ErrNoRows)
		q.EXPECT().GetSubscriptionPlan(gomock.Any(), planID).Return(testPlan, nil)
		q.EXPECT().CountManagerUnits(gomock.Any(), gomock.Any()).Return(int32(0), nil)
		q.EXPECT().CreateSubscription(gomock.Any(), gomock.Any()).Return(sqlc.Subscription{ID: uuid.New(), PlanID: planID, Status: SubscriptionPastDue}, nil)
		q.EXPECT().CreateSubscriptionInvoice(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ any, a sqlc.CreateSubscriptionInvoiceParams) (sqlc.SubscriptionInvoice, error) {
				return sqlc.SubscriptionInvoice{ID: uuid.New(), ManagerID: a.ManagerID, Amount: a.Amount, MpesaReceipt: a.MpesaReceipt}, nil
			})
		q.EXPECT().GetManagerBillingContact(gomock.Any(), gomock.Any()).Return(sqlc.GetManagerBillingContactRow{FirmName: "Acme", Phone: "+254700000000"}, nil)

		renewal, err := m.StartRenewal(context.Background(), tenantID, &planID)
		if err != nil {
			t.Fatalf("StartRenewal: %v", err)
		}
		if renewal.Invoice.Amount != moneyKES("10.00") {
			t.Errorf("amount = %s, want 10.00", renewal.Invoice.Amount)
		}
	})

	t.Run("an archived plan is refused like an unknown plan", func(t *testing.T) {
		m, q, _ := newTestBillingModel(t, "development")
		archivedAt := time.Now()
		archived := sqlc.SubscriptionPlan{ID: planID, Name: "Old", Price: moneyKES("1500.00"), BillingInterval: "monthly", ArchivedAt: &archivedAt}
		q.EXPECT().GetSubscriptionByManager(gomock.Any(), gomock.Any()).Return(sqlc.Subscription{}, sql.ErrNoRows)
		q.EXPECT().GetSubscriptionPlan(gomock.Any(), planID).Return(archived, nil)

		_, err := m.StartRenewal(context.Background(), uuid.New(), &planID)
		if !errors.Is(err, ErrNoPlan) {
			t.Fatalf("err = %v, want ErrNoPlan", err)
		}
	})
}

func TestPlanPriceForUnits(t *testing.T) {
	perUnit := moneyKES("45.00")
	min := moneyKES("7500.00")
	plan := Plan{PricingType: PricingPerUnit, PerUnitPrice: &perUnit, MinPrice: &min}

	// Below the minimum: the floor applies (Kimani & Associates, 20 units).
	if got := plan.PriceForUnits(20); got != min {
		t.Errorf("PriceForUnits(20) = %s, want the 7,500 floor", got)
	}
	// Above the minimum: per-unit pricing applies (200 units × 45 = 9,000).
	if got := plan.PriceForUnits(200); got != moneyKES("9000.00") {
		t.Errorf("PriceForUnits(200) = %s, want 9,000", got)
	}

	flat := Plan{PricingType: PricingFlat, Price: moneyKES("1500.00")}
	if got := flat.PriceForUnits(500); got != moneyKES("1500.00") {
		t.Errorf("a flat plan ignores units: got %s", got)
	}
}

func TestProcessSubscriptionPaymentTestPlanGuard(t *testing.T) {
	invoiceID, planID, managerID := uuid.New(), uuid.New(), uuid.New()
	testPlan := sqlc.SubscriptionPlan{ID: planID, Name: "TEST - KES 10", Price: moneyKES("10.00"), BillingInterval: "monthly", IsTest: true}

	m, q, store := newTestBillingModel(t, "production")
	// ResolveSubscriptionInvoice runs on the Resolver's store directly (a
	// cross-tenant, BYPASSRLS lookup), outside any tenant transaction.
	store.EXPECT().ResolveSubscriptionInvoice(gomock.Any(), invoiceID).Return(sqlc.ResolveSubscriptionInvoiceRow{ID: invoiceID, ManagerID: managerID}, nil)
	q.EXPECT().GetSubscriptionInvoice(gomock.Any(), gomock.Any()).Return(sqlc.SubscriptionInvoice{ID: invoiceID, PlanID: planID, Amount: moneyKES("10.00"), Status: InvoicePending}, nil)
	q.EXPECT().MarkSubscriptionInvoicePaid(gomock.Any(), gomock.Any()).Return(int64(1), nil)
	q.EXPECT().GetSubscriptionByManager(gomock.Any(), gomock.Any()).Return(sqlc.Subscription{ID: uuid.New(), Status: SubscriptionPastDue}, nil)
	q.EXPECT().GetSubscriptionPlan(gomock.Any(), planID).Return(testPlan, nil)
	// ActivateSubscription must never be called: the environment flipped to
	// non-development between renewal and this callback.

	_, err := m.ProcessSubscriptionPayment(context.Background(), &payhero.SubscriptionsWebhookPayload{
		Reference: invoiceID.String(), Amount: "10.00", MpesaReceipt: "SUB1", Success: true,
	})
	if !errors.Is(err, ErrPlanNotAvailable) {
		t.Fatalf("err = %v, want ErrPlanNotAvailable", err)
	}
}
