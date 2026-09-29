package main

import (
	"database/sql"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/codercollo/willcoll/backend/internal/db/sqlc"
	"github.com/codercollo/willcoll/backend/internal/payhero"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/mock/gomock"
)

func TestRenewSubscriptionHandler(t *testing.T) {
	planID := uuid.New()
	invoiceID := uuid.New()
	plan := func(price string) sqlc.SubscriptionPlan {
		return sqlc.SubscriptionPlan{ID: planID, Name: "Standard", Price: money(price), BillingInterval: "monthly"}
	}
	// expectRenewal sets up a manager with no subscription renewing plan planID.
	expectRenewal := func(p *payTestApp, price string) {
		p.q.EXPECT().GetSubscriptionByManager(gomock.Any(), gomock.Any()).Return(sqlc.Subscription{}, sql.ErrNoRows)
		p.q.EXPECT().GetSubscriptionPlan(gomock.Any(), planID).Return(plan(price), nil)
		p.q.EXPECT().CountManagerUnits(gomock.Any(), gomock.Any()).Return(int32(0), nil)
		p.q.EXPECT().CreateSubscription(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ any, a sqlc.CreateSubscriptionParams) (sqlc.Subscription, error) {
				if a.Status != "past_due" {
					t.Errorf("a new subscription must start unpaid, got %q", a.Status)
				}
				return sqlc.Subscription{ID: uuid.New(), ManagerID: a.ManagerID, PlanID: a.PlanID, Status: a.Status}, nil
			})
		p.q.EXPECT().CreateSubscriptionInvoice(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ any, a sqlc.CreateSubscriptionInvoiceParams) (sqlc.SubscriptionInvoice, error) {
				if !strings.HasPrefix(a.MpesaReceipt, "PENDING-") {
					t.Errorf("placeholder receipt = %q", a.MpesaReceipt)
				}
				return sqlc.SubscriptionInvoice{ID: invoiceID, ManagerID: a.ManagerID, Amount: a.Amount, Status: "pending", MpesaReceipt: a.MpesaReceipt}, nil
			})
		p.q.EXPECT().GetManagerBillingContact(gomock.Any(), gomock.Any()).Return(sqlc.GetManagerBillingContactRow{FirmName: "Acme Managers", Phone: "+254700000000"}, nil)
	}
	body := `{"plan_id":"` + planID.String() + `"}`

	t.Run("opens an invoice and prompts the manager on the billing channel", func(t *testing.T) {
		p := newPayTestApp(t)
		expectRenewal(p, "2500.00")
		w := serveAsManager(p.app.renewSubscriptionHandler, http.MethodPost, "/", body, nil)
		if w.Code != http.StatusAccepted || !strings.Contains(w.Body.String(), invoiceID.String()) {
			t.Fatalf("status %d: %s", w.Code, w.Body)
		}
		if strings.Contains(w.Body.String(), "PENDING-") {
			t.Error("the placeholder receipt must not be exposed")
		}
		if len(p.push.calls) != 1 {
			t.Fatalf("pushes = %d", len(p.push.calls))
		}
		c := p.push.calls[0]
		if c.Amount != 2500 || c.ChannelID != "BILLING-CH" || c.ExternalReference != invoiceID.String() || c.PhoneNumber != "254700000000" ||
			c.CallbackURL != "https://api.example.com/v1/webhooks/payhero/subscriptions?token="+testWebhookSecret {
			t.Errorf("push = %+v (billing channel, the invoice id as reference, the subscriptions callback)", c)
		}
	})

	t.Run("a failed push fails the invoice and does not leak PayHero", func(t *testing.T) {
		p := newPayTestApp(t)
		expectRenewal(p, "2500.00")
		p.push.err = &payhero.APIError{StatusCode: 500, Body: "internal secret detail"}
		p.q.EXPECT().MarkSubscriptionInvoiceFailed(gomock.Any(), gomock.Any()).Return(int64(1), nil)
		w := serveAsManager(p.app.renewSubscriptionHandler, http.MethodPost, "/", body, nil)
		if w.Code != http.StatusBadGateway || strings.Contains(w.Body.String(), "secret detail") {
			t.Errorf("status %d: %s", w.Code, w.Body)
		}
	})

	t.Run("a price with cents cannot be paid by STK", func(t *testing.T) {
		p := newPayTestApp(t)
		expectRenewal(p, "2500.50")
		p.q.EXPECT().MarkSubscriptionInvoiceFailed(gomock.Any(), gomock.Any()).Return(int64(1), nil)
		w := serveAsManager(p.app.renewSubscriptionHandler, http.MethodPost, "/", body, nil)
		if w.Code != http.StatusUnprocessableEntity || len(p.push.calls) != 0 {
			t.Errorf("status %d, pushes %d", w.Code, len(p.push.calls))
		}
	})

	t.Run("a manager with no subscription must choose a plan", func(t *testing.T) {
		p := newPayTestApp(t)
		p.q.EXPECT().GetSubscriptionByManager(gomock.Any(), gomock.Any()).Return(sqlc.Subscription{}, sql.ErrNoRows)
		w := serveAsManager(p.app.renewSubscriptionHandler, http.MethodPost, "/", "", nil)
		if w.Code != http.StatusUnprocessableEntity || len(p.push.calls) != 0 {
			t.Errorf("status %d: %s", w.Code, w.Body)
		}
	})

	t.Run("billing not configured is a 503 before any work", func(t *testing.T) {
		p := newPayTestApp(t)
		p.app.config.payhero.billingChannelID = ""
		if w := serveAsManager(p.app.renewSubscriptionHandler, http.MethodPost, "/", body, nil); w.Code != http.StatusServiceUnavailable {
			t.Errorf("status %d", w.Code)
		}
	})

	t.Run("an explicit phone overrides the manager's stored phone", func(t *testing.T) {
		p := newPayTestApp(t)
		expectRenewal(p, "2500.00")
		overrideBody := `{"plan_id":"` + planID.String() + `","phone":"0711000000"}`
		w := serveAsManager(p.app.renewSubscriptionHandler, http.MethodPost, "/", overrideBody, nil)
		if w.Code != http.StatusAccepted {
			t.Fatalf("status %d: %s", w.Code, w.Body)
		}
		if len(p.push.calls) != 1 || p.push.calls[0].PhoneNumber != "254711000000" {
			t.Errorf("push phone = %+v, want the override normalized, not the stored contact phone", p.push.calls)
		}
	})

	t.Run("an invalid phone override is refused before any push", func(t *testing.T) {
		p := newPayTestApp(t)
		expectRenewal(p, "2500.00")
		p.q.EXPECT().MarkSubscriptionInvoiceFailed(gomock.Any(), gomock.Any()).Return(int64(1), nil)
		badBody := `{"plan_id":"` + planID.String() + `","phone":"12345"}`
		w := serveAsManager(p.app.renewSubscriptionHandler, http.MethodPost, "/", badBody, nil)
		if w.Code != http.StatusUnprocessableEntity || len(p.push.calls) != 0 {
			t.Errorf("status %d, pushes %d", w.Code, len(p.push.calls))
		}
	})

	t.Run("the chosen plan's own price is charged, not another plan's", func(t *testing.T) {
		// Two distinct plans exist (e.g. "Standard" at 2500 and a dev
		// "TEST - KES 10" at 10); renewing with the TEST plan's id must
		// price the push at 10, never at the other plan's cached price.
		p := newPayTestApp(t)
		testPlanID := uuid.New()
		testInvoiceID := uuid.New()
		p.q.EXPECT().GetSubscriptionByManager(gomock.Any(), gomock.Any()).Return(sqlc.Subscription{}, sql.ErrNoRows)
		p.q.EXPECT().GetSubscriptionPlan(gomock.Any(), testPlanID).Return(
			sqlc.SubscriptionPlan{ID: testPlanID, Name: "TEST - KES 10", Price: money("10.00"), BillingInterval: "monthly"}, nil)
		p.q.EXPECT().CountManagerUnits(gomock.Any(), gomock.Any()).Return(int32(0), nil)
		p.q.EXPECT().CreateSubscription(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ any, a sqlc.CreateSubscriptionParams) (sqlc.Subscription, error) {
				return sqlc.Subscription{ID: uuid.New(), ManagerID: a.ManagerID, PlanID: a.PlanID, Status: a.Status}, nil
			})
		p.q.EXPECT().CreateSubscriptionInvoice(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ any, a sqlc.CreateSubscriptionInvoiceParams) (sqlc.SubscriptionInvoice, error) {
				return sqlc.SubscriptionInvoice{ID: testInvoiceID, ManagerID: a.ManagerID, Amount: a.Amount, Status: "pending", MpesaReceipt: a.MpesaReceipt}, nil
			})
		p.q.EXPECT().GetManagerBillingContact(gomock.Any(), gomock.Any()).Return(sqlc.GetManagerBillingContactRow{FirmName: "Acme Managers", Phone: "+254700000000"}, nil)

		testBody := `{"plan_id":"` + testPlanID.String() + `"}`
		w := serveAsManager(p.app.renewSubscriptionHandler, http.MethodPost, "/", testBody, nil)
		if w.Code != http.StatusAccepted {
			t.Fatalf("status %d: %s", w.Code, w.Body)
		}
		if len(p.push.calls) != 1 || p.push.calls[0].Amount != 10 {
			t.Fatalf("push = %+v, want amount 10 for the TEST plan, not the Standard plan's 2500", p.push.calls)
		}
	})

	t.Run("switching to a different plan on renewal prices the new plan, not the current one", func(t *testing.T) {
		p := newPayTestApp(t)
		current := uuid.New()
		newInvoiceID := uuid.New()
		sub := sqlc.Subscription{ID: uuid.New(), PlanID: current, Status: "active"}
		p.q.EXPECT().GetSubscriptionByManager(gomock.Any(), gomock.Any()).Return(sub, nil)
		p.q.EXPECT().GetSubscriptionPlan(gomock.Any(), planID).Return(plan("3500.00"), nil)
		p.q.EXPECT().CountManagerUnits(gomock.Any(), gomock.Any()).Return(int32(0), nil)
		p.q.EXPECT().CreateSubscriptionInvoice(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ any, a sqlc.CreateSubscriptionInvoiceParams) (sqlc.SubscriptionInvoice, error) {
				if a.PlanID != planID {
					t.Errorf("invoice plan_id = %s, want the newly chosen plan %s", a.PlanID, planID)
				}
				return sqlc.SubscriptionInvoice{ID: newInvoiceID, ManagerID: a.ManagerID, PlanID: a.PlanID, Amount: a.Amount, Status: "pending", MpesaReceipt: a.MpesaReceipt}, nil
			})
		p.q.EXPECT().GetManagerBillingContact(gomock.Any(), gomock.Any()).Return(sqlc.GetManagerBillingContactRow{FirmName: "Acme Managers", Phone: "+254700000000"}, nil)

		w := serveAsManager(p.app.renewSubscriptionHandler, http.MethodPost, "/", body, nil)
		if w.Code != http.StatusAccepted {
			t.Fatalf("status %d: %s", w.Code, w.Body)
		}
		if len(p.push.calls) != 1 || p.push.calls[0].Amount != 3500 {
			t.Fatalf("push = %+v, want amount 3500 for the newly chosen plan", p.push.calls)
		}
	})

	t.Run("a plan whose unit cap is below the manager's current units is refused before any push", func(t *testing.T) {
		p := newPayTestApp(t)
		current := uuid.New()
		sub := sqlc.Subscription{ID: uuid.New(), PlanID: current, Status: "active"}
		smallPlan := sqlc.SubscriptionPlan{ID: planID, Name: "Starter", Price: money("1000.00"), BillingInterval: "monthly", UnitCap: sql.NullInt32{Int32: 5, Valid: true}}
		p.q.EXPECT().GetSubscriptionByManager(gomock.Any(), gomock.Any()).Return(sub, nil)
		p.q.EXPECT().GetSubscriptionPlan(gomock.Any(), planID).Return(smallPlan, nil)
		p.q.EXPECT().CountManagerUnits(gomock.Any(), gomock.Any()).Return(int32(8), nil)

		w := serveAsManager(p.app.renewSubscriptionHandler, http.MethodPost, "/", body, nil)
		if w.Code != http.StatusUnprocessableEntity || len(p.push.calls) != 0 ||
			!strings.Contains(w.Body.String(), "Your 8 units exceed Starter's limit of 5") {
			t.Errorf("status %d, pushes %d: %s", w.Code, len(p.push.calls), w.Body)
		}
	})

	testPlan := sqlc.SubscriptionPlan{ID: planID, Name: "TEST - KES 10", Price: money("10.00"), BillingInterval: "monthly", IsTest: true}

	t.Run("a test plan can be renewed onto in development", func(t *testing.T) {
		p := newPayTestApp(t) // development, per newPayTestApp
		p.q.EXPECT().GetSubscriptionByManager(gomock.Any(), gomock.Any()).Return(sqlc.Subscription{}, sql.ErrNoRows)
		p.q.EXPECT().GetSubscriptionPlan(gomock.Any(), planID).Return(testPlan, nil)
		p.q.EXPECT().CountManagerUnits(gomock.Any(), gomock.Any()).Return(int32(0), nil)
		p.q.EXPECT().CreateSubscription(gomock.Any(), gomock.Any()).Return(sqlc.Subscription{ID: uuid.New(), PlanID: planID, Status: "past_due"}, nil)
		p.q.EXPECT().CreateSubscriptionInvoice(gomock.Any(), gomock.Any()).Return(sqlc.SubscriptionInvoice{ID: invoiceID, Amount: money("10.00"), MpesaReceipt: "PENDING-x"}, nil)
		p.q.EXPECT().GetManagerBillingContact(gomock.Any(), gomock.Any()).Return(sqlc.GetManagerBillingContactRow{FirmName: "Acme Managers", Phone: "+254700000000"}, nil)

		w := serveAsManager(p.app.renewSubscriptionHandler, http.MethodPost, "/", body, nil)
		if w.Code != http.StatusAccepted {
			t.Fatalf("status %d: %s", w.Code, w.Body)
		}
	})

	for _, env := range []string{"staging", "production", ""} {
		t.Run("a test plan cannot be renewed onto outside development ("+env+")", func(t *testing.T) {
			p := newPayTestApp(t)
			p.app.config.env, p.app.models.Billing.Env = env, env
			p.q.EXPECT().GetSubscriptionByManager(gomock.Any(), gomock.Any()).Return(sqlc.Subscription{}, sql.ErrNoRows)
			p.q.EXPECT().GetSubscriptionPlan(gomock.Any(), planID).Return(testPlan, nil)

			w := serveAsManager(p.app.renewSubscriptionHandler, http.MethodPost, "/", body, nil)
			if w.Code != http.StatusUnprocessableEntity || len(p.push.calls) != 0 || !strings.Contains(w.Body.String(), "plan not available") {
				t.Errorf("status %d, pushes %d: %s", w.Code, len(p.push.calls), w.Body)
			}
		})
	}
}

func TestListSubscriptionPlansHandler(t *testing.T) {
	t.Run("includes the TEST plan in development", func(t *testing.T) {
		p := newPayTestApp(t) // development, per newPayTestApp
		p.q.EXPECT().ListSubscriptionPlans(gomock.Any(), true).Return([]sqlc.SubscriptionPlan{
			{ID: uuid.New(), Name: "TEST - KES 10", Price: money("10"), BillingInterval: "monthly", IsTest: true},
		}, nil)
		p.q.EXPECT().CountManagerUnits(gomock.Any(), gomock.Any()).Return(int32(0), nil)
		w := serveAsManager(p.app.listSubscriptionPlansHandler, http.MethodGet, "/", "", nil)
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "TEST - KES 10") {
			t.Fatalf("status %d: %s", w.Code, w.Body)
		}
	})

	for _, env := range []string{"staging", "production", ""} {
		t.Run("excludes the TEST plan outside development ("+env+")", func(t *testing.T) {
			p := newPayTestApp(t)
			p.app.config.env, p.app.models.Billing.Env = env, env
			p.q.EXPECT().ListSubscriptionPlans(gomock.Any(), false).Return([]sqlc.SubscriptionPlan{
				{ID: uuid.New(), Name: "Starter", Price: money("1500"), BillingInterval: "monthly"},
			}, nil)
			p.q.EXPECT().CountManagerUnits(gomock.Any(), gomock.Any()).Return(int32(0), nil)
			w := serveAsManager(p.app.listSubscriptionPlansHandler, http.MethodGet, "/", "", nil)
			if w.Code != http.StatusOK || strings.Contains(w.Body.String(), "TEST") {
				t.Fatalf("status %d: %s", w.Code, w.Body)
			}
		})
	}
}

func TestSubscriptionInvoicePDFHandler(t *testing.T) {
	p := newPayTestApp(t)
	id := uuid.New()
	p.q.EXPECT().GetSubscriptionInvoice(gomock.Any(), gomock.Any()).Return(sqlc.SubscriptionInvoice{
		ID: id, Amount: money("2500"), Period: sep2026, Status: "paid", MpesaReceipt: "SUB1", CreatedAt: time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC),
	}, nil)
	p.q.EXPECT().GetManagerBillingContact(gomock.Any(), gomock.Any()).Return(sqlc.GetManagerBillingContactRow{FirmName: "Acme Managers", Email: "a@acme.co.ke"}, nil)
	p.q.EXPECT().GetSubscriptionByManager(gomock.Any(), gomock.Any()).Return(sqlc.Subscription{PlanID: uuid.New()}, nil)
	p.q.EXPECT().GetSubscriptionPlan(gomock.Any(), gomock.Any()).Return(sqlc.SubscriptionPlan{Name: "Standard"}, nil)

	w := serveAsManager(p.app.showSubscriptionInvoicePDFHandler, http.MethodGet, "/", "", gin.Params{{Key: "id", Value: id.String()}})
	if w.Code != http.StatusOK || w.Header().Get("Content-Type") != "application/pdf" || !strings.HasPrefix(w.Body.String(), "%PDF") {
		t.Fatalf("status %d type %q", w.Code, w.Header().Get("Content-Type"))
	}
	for _, want := range []string{"WILLCOLL", "INV-" + strings.ToUpper(id.String()[:8]), "Acme Managers", "2,500.00", "Standard"} {
		if !strings.Contains(w.Body.String(), want) {
			t.Errorf("invoice PDF missing %q", want)
		}
	}

	notMine := newPayTestApp(t)
	notMine.q.EXPECT().GetSubscriptionInvoice(gomock.Any(), gomock.Any()).Return(sqlc.SubscriptionInvoice{}, sql.ErrNoRows)
	if w := serveAsManager(notMine.app.showSubscriptionInvoicePDFHandler, http.MethodGet, "/", "", gin.Params{{Key: "id", Value: id.String()}}); w.Code != http.StatusNotFound {
		t.Errorf("another manager invoice: status %d, want 404", w.Code)
	}
}

func TestPaymentReviewHandlers(t *testing.T) {
	paymentID := uuid.New()
	params := gin.Params{{Key: "id", Value: paymentID.String()}}
	payment := sqlc.Payment{ID: paymentID, Amount: money("3000"), Status: "unmatched"}
	allocBody := func(a1, a2 string) string {
		return `{"unit_id":"` + testUnitA.String() + `","allocations":[{"ledger_type":"rent","amount":"` + a1 + `"},{"ledger_type":"water","amount":"` + a2 + `"}]}`
	}
	expectPlaceable := func(p *payTestApp, already string) {
		p.q.EXPECT().GetPayment(gomock.Any(), gomock.Any()).Return(payment, nil)
		p.q.EXPECT().GetUnit(gomock.Any(), gomock.Any()).Return(sqlc.Unit{ID: testUnitA}, nil)
		p.q.EXPECT().SumPaymentAllocations(gomock.Any(), gomock.Any()).Return(money(already), nil)
	}

	t.Run("a manager places an unmatched payment, attributed to them", func(t *testing.T) {
		p := newPayTestApp(t)
		expectPlaceable(p, "0")
		expectReceiptIssuance(p.q, testUnitA)
		p.q.EXPECT().CreateTransactionHeader(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ any, a sqlc.CreateTransactionHeaderParams) (uuid.UUID, error) {
				if a.CreatedBy != testTenantID.String() {
					t.Errorf("created_by = %q, want the manager", a.CreatedBy)
				}
				return uuid.New(), nil
			})
		p.q.EXPECT().GetOrCreateLedgerAccount(gomock.Any(), gomock.Any()).Return(uuid.New(), nil).Times(2)
		p.q.EXPECT().CreateLedgerEntry(gomock.Any(), gomock.Any()).Return(uuid.New(), nil).Times(2)
		p.q.EXPECT().CreatePaymentAllocation(gomock.Any(), gomock.Any()).Return(nil).Times(2)
		p.q.EXPECT().SumPaymentAllocations(gomock.Any(), gomock.Any()).Return(money("3000"), nil)
		p.q.EXPECT().SetPaymentMatch(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ any, a sqlc.SetPaymentMatchParams) error {
				if a.Status != "allocated" || a.AutoAppliedUnconfirmed {
					t.Errorf("match = %+v", a)
				}
				return nil
			})
		if w := serveAsManager(p.app.allocatePaymentHandler, http.MethodPost, "/", allocBody("2000", "1000"), params); w.Code != http.StatusOK {
			t.Fatalf("status %d: %s", w.Code, w.Body)
		}
	})

	t.Run("a split that does not add up is refused before anything posts", func(t *testing.T) {
		p := newPayTestApp(t)
		expectPlaceable(p, "0")
		w := serveAsManager(p.app.allocatePaymentHandler, http.MethodPost, "/", allocBody("2000", "500"), params)
		if w.Code != http.StatusUnprocessableEntity || !strings.Contains(w.Body.String(), "3000.00") {
			t.Errorf("status %d: %s", w.Code, w.Body)
		}
	})

	t.Run("an already-allocated payment cannot be placed again", func(t *testing.T) {
		p := newPayTestApp(t)
		expectPlaceable(p, "3000")
		if w := serveAsManager(p.app.allocatePaymentHandler, http.MethodPost, "/", allocBody("2000", "1000"), params); w.Code != http.StatusConflict {
			t.Errorf("status %d", w.Code)
		}
	})

	t.Run("unknown payment is a 404", func(t *testing.T) {
		p := newPayTestApp(t)
		p.q.EXPECT().GetPayment(gomock.Any(), gomock.Any()).Return(sqlc.Payment{}, sql.ErrNoRows)
		if w := serveAsManager(p.app.allocatePaymentHandler, http.MethodPost, "/", allocBody("2000", "1000"), params); w.Code != http.StatusNotFound {
			t.Errorf("status %d", w.Code)
		}
	})

	for name, body := range map[string]string{
		"no allocations":   `{"unit_id":"` + testUnitA.String() + `","allocations":[]}`,
		"unknown ledger":   `{"unit_id":"` + testUnitA.String() + `","allocations":[{"ledger_type":"tax","amount":"3000"}]}`,
		"duplicate ledger": `{"unit_id":"` + testUnitA.String() + `","allocations":[{"ledger_type":"rent","amount":"1"},{"ledger_type":"RENT","amount":"2"}]}`,
		"zero amount":      `{"unit_id":"` + testUnitA.String() + `","allocations":[{"ledger_type":"rent","amount":"0"}]}`,
		"missing unit":     `{"allocations":[{"ledger_type":"rent","amount":"3000"}]}`,
	} {
		t.Run("rejects "+name, func(t *testing.T) {
			p := newPayTestApp(t)
			if w := serveAsManager(p.app.allocatePaymentHandler, http.MethodPost, "/", body, params); w.Code != http.StatusUnprocessableEntity {
				t.Errorf("status %d: %s", w.Code, w.Body)
			}
		})
	}

	t.Run("confirm clears the flag, and refuses when there is nothing to confirm", func(t *testing.T) {
		p := newPayTestApp(t)
		p.q.EXPECT().GetPayment(gomock.Any(), gomock.Any()).Return(payment, nil).Times(2)
		gomock.InOrder(
			p.q.EXPECT().ConfirmPayment(gomock.Any(), gomock.Any()).Return(int64(1), nil),
			p.q.EXPECT().ConfirmPayment(gomock.Any(), gomock.Any()).Return(int64(0), nil),
		)
		if w := serveAsManager(p.app.confirmPaymentHandler, http.MethodPost, "/", "", params); w.Code != http.StatusOK {
			t.Errorf("first confirm: %d", w.Code)
		}
		if w := serveAsManager(p.app.confirmPaymentHandler, http.MethodPost, "/", "", params); w.Code != http.StatusConflict {
			t.Errorf("second confirm: %d, want 409", w.Code)
		}
	})

	t.Run("the review list", func(t *testing.T) {
		p := newPayTestApp(t)
		code := "A1"
		note := "the payer matches more than one unit"
		p.q.EXPECT().ListPaymentsForReview(gomock.Any(), gomock.Any()).Return([]sqlc.ListPaymentsForReviewRow{
			{TotalRecords: 1, ID: paymentID, Source: "payhero_c2b", MpesaReceipt: "R1", Amount: money("3000"), Msisdn: "254700000000",
				Status: "unmatched", ReviewNote: &note, UnitCode: &code, ReceivedAt: time.Now()},
		}, nil)
		w := serveAsManager(p.app.listPaymentsForReviewHandler, http.MethodGet, "/", "", nil)
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), note) || !strings.Contains(w.Body.String(), `"unit_code": "A1"`) {
			t.Errorf("status %d: %s", w.Code, w.Body)
		}
	})
}
