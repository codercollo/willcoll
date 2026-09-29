package data

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/codercollo/willcoll/backend/internal/payhero"
	"github.com/google/uuid"
)

// biModels wires every model against the test databases: willcoll_app for
// tenant work and willcoll_admin for the cross-tenant resolver lookups.
func biModels(t *testing.T) (Models, *sql.DB, *sql.DB) {
	t.Helper()
	conn := openTestDB(t)
	adminDSN := os.Getenv("WILLCOLL_TEST_ADMIN_DB_DSN")
	if adminDSN == "" {
		t.Skip("WILLCOLL_TEST_ADMIN_DB_DSN not set; skipping")
	}
	adminConn, err := sql.Open("pgx", adminDSN)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { adminConn.Close() })
	return NewModels(conn, adminConn, integrationTimeout), conn, adminConn
}

// TestSubscriptionIntegration drives platform billing: each manager is a
// tenant with their own subscription and invoices, and the PayHero callback
// finds the manager from the invoice reference alone.
func TestSubscriptionIntegration(t *testing.T) {
	models, conn, adminConn := biModels(t)
	ctx := context.Background()
	models.Billing.Now = func() time.Time { return time.Date(2026, 9, 10, 9, 0, 0, 0, time.UTC) }

	managerA, _ := seedTenant(t, conn)
	managerB, _ := seedTenant(t, conn)

	planID := uuid.New()
	// Plans are managed by the platform admin: the API role cannot write them.
	if _, err := adminConn.Exec(`INSERT INTO subscription_plans (id, name, price, billing_interval) VALUES ($1, 'Test Monthly', 2500, 'monthly')`, planID); err != nil {
		t.Fatal(err)
	}

	ok := func(ref, receipt, amount string) *payhero.SubscriptionsWebhookPayload {
		return &payhero.SubscriptionsWebhookPayload{Success: true, Reference: ref, MpesaReceipt: receipt, Amount: amount}
	}

	// --- Renewing with no subscription needs a plan.
	if _, err := models.Billing.StartRenewal(ctx, managerA, nil); !errors.Is(err, ErrNoPlan) {
		t.Fatalf("no plan chosen: err = %v, want ErrNoPlan", err)
	}
	bogus := uuid.New()
	if _, err := models.Billing.StartRenewal(ctx, managerA, &bogus); !errors.Is(err, ErrNoPlan) {
		t.Errorf("unknown plan: err = %v, want ErrNoPlan", err)
	}
	if _, err := models.Billing.Current(ctx, managerA); !errors.Is(err, ErrRecordNotFound) {
		t.Errorf("no subscription yet: err = %v", err)
	}

	ren, err := models.Billing.StartRenewal(ctx, managerA, &planID)
	if err != nil {
		t.Fatal(err)
	}
	if ren.Invoice.Status != InvoicePending || ren.Invoice.Amount != biMoney(t, "2500") || ren.Phone == "" || ren.Invoice.MpesaReceipt != "" {
		t.Errorf("renewal = %+v (a pending invoice must not expose its placeholder receipt)", ren)
	}
	cur, err := models.Billing.Current(ctx, managerA)
	if err != nil || cur.Subscription.Status != SubscriptionPastDue || cur.Plan.Name != "Test Monthly" {
		t.Fatalf("subscription before payment = %+v, %v", cur, err)
	}

	// --- Multi-tenancy: another manager sees nothing of A.
	if _, err := models.Billing.InvoiceForPDF(ctx, managerB, ren.Invoice.ID); !errors.Is(err, ErrRecordNotFound) {
		t.Errorf("another manager reading the invoice: err = %v, want ErrRecordNotFound", err)
	}
	if invs, _, err := models.Billing.Invoices(ctx, managerB, Filters{Page: 1, PageSize: 20, Sort: "-created_at"}); err != nil || len(invs) != 0 {
		t.Errorf("manager B invoices = %v, %v", invs, err)
	}

	// --- Underpaid: refused, invoice stays pending, nothing activated.
	if _, err := models.Billing.ProcessSubscriptionPayment(ctx, ok(ren.Invoice.ID.String(), "SUB-LOW", "1000.00")); !errors.Is(err, ErrUnderpaid) {
		t.Errorf("underpayment: err = %v, want ErrUnderpaid", err)
	}
	if cur, _ := models.Billing.Current(ctx, managerA); cur.Subscription.Status != SubscriptionPastDue {
		t.Errorf("underpayment must not activate: %s", cur.Subscription.Status)
	}

	// --- Paid: subscription active for one interval from today.
	res, err := models.Billing.ProcessSubscriptionPayment(ctx, ok(ren.Invoice.ID.String(), "SUB-001", "2500.00"))
	if err != nil || !res.Paid || res.Duplicate || res.ManagerID != managerA {
		t.Fatalf("payment = %+v, %v", res, err)
	}
	wantEnd := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	cur, _ = models.Billing.Current(ctx, managerA)
	if cur.Subscription.Status != SubscriptionActive || !cur.Subscription.CurrentPeriodEnd.Equal(wantEnd) {
		t.Errorf("subscription after payment = %+v, want active until %s", cur.Subscription, wantEnd)
	}

	// --- Redelivery changes nothing.
	dup, err := models.Billing.ProcessSubscriptionPayment(ctx, ok(ren.Invoice.ID.String(), "SUB-001", "2500.00"))
	if err != nil || !dup.Duplicate || dup.Paid {
		t.Errorf("redelivery = %+v, %v", dup, err)
	}
	if cur, _ := models.Billing.Current(ctx, managerA); !cur.Subscription.CurrentPeriodEnd.Equal(wantEnd) {
		t.Errorf("redelivery extended the period to %s", cur.Subscription.CurrentPeriodEnd)
	}

	// --- Renewing while active extends from the end of the running period.
	ren2, err := models.Billing.StartRenewal(ctx, managerA, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := models.Billing.ProcessSubscriptionPayment(ctx, ok(ren2.Invoice.ID.String(), "SUB-002", "2500")); err != nil {
		t.Fatal(err)
	}
	cur, _ = models.Billing.Current(ctx, managerA)
	if want := time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC); !cur.Subscription.CurrentPeriodEnd.Equal(want) {
		t.Errorf("renewal while active: period end %s, want %s", cur.Subscription.CurrentPeriodEnd, want)
	}

	// --- A receipt already used on another invoice is a duplicate, not a second credit.
	ren3, _ := models.Billing.StartRenewal(ctx, managerA, nil)
	if dupReceipt, err := models.Billing.ProcessSubscriptionPayment(ctx, ok(ren3.Invoice.ID.String(), "SUB-002", "2500")); err != nil || !dupReceipt.Duplicate {
		t.Errorf("reused receipt = %+v, %v", dupReceipt, err)
	}

	// --- Failed push: pending invoice fails; a paid one is left alone.
	if f, err := models.Billing.ProcessSubscriptionPayment(ctx, &payhero.SubscriptionsWebhookPayload{Reference: ren3.Invoice.ID.String()}); err != nil || !f.Failed {
		t.Errorf("failed callback = %+v, %v", f, err)
	}
	if f, err := models.Billing.ProcessSubscriptionPayment(ctx, &payhero.SubscriptionsWebhookPayload{Reference: ren.Invoice.ID.String()}); err != nil || f.Failed {
		t.Errorf("a paid invoice must not be failed: %+v, %v", f, err)
	}

	// --- Unknown or malformed references.
	if _, err := models.Billing.ProcessSubscriptionPayment(ctx, ok(uuid.NewString(), "X", "1")); !errors.Is(err, ErrRecordNotFound) {
		t.Errorf("unknown invoice: err = %v", err)
	}
	if _, err := models.Billing.ProcessSubscriptionPayment(ctx, ok("not-a-uuid", "X", "1")); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("malformed reference: err = %v", err)
	}

	// --- Concurrent duplicate callbacks settle the invoice once.
	ren4, _ := models.Billing.StartRenewal(ctx, managerA, nil)
	before, _ := models.Billing.Current(ctx, managerA)
	var wg sync.WaitGroup
	results := make([]*SubscriptionResult, 4)
	errs := make([]error, 4)
	for i := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i], errs[i] = models.Billing.ProcessSubscriptionPayment(ctx, ok(ren4.Invoice.ID.String(), "SUB-RACE", "2500"))
		}()
	}
	wg.Wait()
	paid := 0
	for i := range results {
		if errs[i] != nil {
			t.Errorf("concurrent callback %d: %v", i, errs[i])
		} else if results[i].Paid {
			paid++
		}
	}
	after, _ := models.Billing.Current(ctx, managerA)
	if paid != 1 || !after.Subscription.CurrentPeriodEnd.Equal(before.Subscription.CurrentPeriodEnd.AddDate(0, 1, 0)) {
		t.Errorf("%d concurrent callbacks paid the invoice (want 1); period end %s -> %s (want one month)", paid,
			before.Subscription.CurrentPeriodEnd, after.Subscription.CurrentPeriodEnd)
	}

	// --- Listing and the PDF source.
	invs, meta, err := models.Billing.Invoices(ctx, managerA, Filters{Page: 1, PageSize: 20, Sort: "-created_at"})
	if err != nil || meta.TotalRecords != 4 || len(invs) != 4 {
		t.Errorf("invoices = %d (%+v), %v, want 4", len(invs), meta, err)
	}
	doc, err := models.Billing.InvoiceForPDF(ctx, managerA, ren.Invoice.ID)
	if err != nil || doc.PlanName != "Test Monthly" || doc.Invoice.MpesaReceipt != "SUB-001" || doc.ManagerName == "" {
		t.Errorf("invoice document = %+v, %v", doc, err)
	}
}

// TestSubscriptionPlanSwitchIntegration: a manager may switch to any active
// plan at renewal, so long as its unit cap covers their current units; the
// switch only takes effect once the webhook confirms payment.
func TestSubscriptionPlanSwitchIntegration(t *testing.T) {
	models, conn, adminConn := biModels(t)
	ctx := context.Background()
	manager, landlord := seedTenant(t, conn)

	property := &Property{LandlordID: landlord, Name: "Switch Towers", Location: "Nairobi", Slug: "sw-" + uuid.NewString()[:8]}
	if err := models.Properties.Insert(ctx, manager, property); err != nil {
		t.Fatal(err)
	}
	for _, code := range []string{"S1", "S2", "S3"} {
		u := &Unit{PropertyID: property.ID, UnitCode: code, Status: "vacant"}
		if err := models.Units.Insert(ctx, manager, u); err != nil {
			t.Fatal(err)
		}
	}

	// Plan names are unique among active plans (subscription_plans_name_active_key),
	// and the migrations seed a production plan set, so these test fixtures
	// need their own unique names, not ones the seed already uses.
	suffix := uuid.NewString()[:8]
	initialID, smallID, bigID := uuid.New(), uuid.New(), uuid.New()
	if _, err := adminConn.Exec(`INSERT INTO subscription_plans (id, name, price, billing_interval, unit_cap) VALUES ($1, $2, 1500, 'monthly', 10)`, initialID, "Initial "+suffix); err != nil {
		t.Fatal(err)
	}
	if _, err := adminConn.Exec(`INSERT INTO subscription_plans (id, name, price, billing_interval, unit_cap) VALUES ($1, $2, 1000, 'monthly', 2)`, smallID, "Small "+suffix); err != nil {
		t.Fatal(err)
	}
	if _, err := adminConn.Exec(`INSERT INTO subscription_plans (id, name, price, billing_interval, unit_cap) VALUES ($1, $2, 3500, 'monthly', 10)`, bigID, "Growth Plus "+suffix); err != nil {
		t.Fatal(err)
	}

	// Get the manager onto an initial plan first, so a later "switch" has
	// something to leave unchanged.
	initRen, err := models.Billing.StartRenewal(ctx, manager, &initialID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := models.Billing.ProcessSubscriptionPayment(ctx, &payhero.SubscriptionsWebhookPayload{
		Success: true, Reference: initRen.Invoice.ID.String(), MpesaReceipt: "SUB-INIT", Amount: "1500",
	}); err != nil {
		t.Fatal(err)
	}

	// The manager already has 3 units: switching to a plan capped at 2 is
	// refused, and nothing is invoiced.
	if _, err := models.Billing.StartRenewal(ctx, manager, &smallID); !errors.Is(err, ErrPlanTooSmall) {
		t.Fatalf("too-small plan: err = %v, want ErrPlanTooSmall", err)
	}

	// Renewing onto the bigger plan opens an invoice priced at that plan,
	// but the subscription itself has not moved yet.
	ren, err := models.Billing.StartRenewal(ctx, manager, &bigID)
	if err != nil || ren.Invoice.Amount != biMoney(t, "3500") || ren.Invoice.PlanID != bigID {
		t.Fatalf("switch renewal = %+v, %v", ren, err)
	}
	cur, err := models.Billing.Current(ctx, manager)
	if err != nil || cur.Plan.ID != initialID {
		t.Fatalf("plan must not switch before payment: %+v, %v", cur, err)
	}

	// Only once the webhook confirms payment does the switch take effect.
	res, err := models.Billing.ProcessSubscriptionPayment(ctx, &payhero.SubscriptionsWebhookPayload{
		Success: true, Reference: ren.Invoice.ID.String(), MpesaReceipt: "SUB-SWITCH", Amount: "3500",
	})
	if err != nil || !res.Paid {
		t.Fatalf("payment = %+v, %v", res, err)
	}
	cur, err = models.Billing.Current(ctx, manager)
	if err != nil || cur.Plan.ID != bigID {
		t.Fatalf("plan after payment = %+v, %v, want %s", cur, err, bigID)
	}
}

// TestPlanManagementIntegration: the Super Admin defines plans (only the admin
// role may write them), managers read them, and a price change only affects
// invoices raised afterwards.
func TestPlanManagementIntegration(t *testing.T) {
	models, conn, _ := biModels(t)
	ctx := context.Background()
	manager, _ := seedTenant(t, conn)

	plan, err := models.Platform.CreatePlan(ctx, PlanInput{Name: "Growth " + uuid.NewString()[:6], Price: biMoney(t, "3000"), BillingInterval: "monthly"}, false)
	if err != nil {
		t.Fatalf("CreatePlan: %v", err)
	}

	plans, _, err := models.Billing.Plans(ctx, manager)
	found := false
	for _, p := range plans {
		found = found || p.ID == plan.ID
	}
	if err != nil || !found {
		t.Fatalf("a manager cannot see the plan the admin created: %v", err)
	}

	ren, err := models.Billing.StartRenewal(ctx, manager, &plan.ID)
	if err != nil || ren.Invoice.Amount != biMoney(t, "3000") {
		t.Fatalf("first invoice = %+v, %v", ren, err)
	}

	updated, err := models.Platform.UpdatePlan(ctx, plan.ID, PlanInput{Name: plan.Name, Price: biMoney(t, "3500"), BillingInterval: "monthly"}, false)
	if err != nil || updated.Price != biMoney(t, "3500") {
		t.Fatalf("UpdatePlan: %+v, %v", updated, err)
	}
	next, err := models.Billing.StartRenewal(ctx, manager, nil)
	if err != nil || next.Invoice.Amount != biMoney(t, "3500") {
		t.Errorf("a new invoice must carry the new price: %+v, %v", next, err)
	}
	doc, err := models.Billing.InvoiceForPDF(ctx, manager, ren.Invoice.ID)
	if err != nil || doc.Invoice.Amount != biMoney(t, "3000") {
		t.Errorf("the invoice already issued must keep its amount: %+v, %v", doc, err)
	}

	listed, err := models.Platform.ListPlans(ctx)
	count := int64(-1)
	for _, p := range listed {
		if p.ID == plan.ID {
			count = p.Subscribers
		}
	}
	if err != nil || count != 1 {
		t.Errorf("subscriber count = %d, %v; want 1", count, err)
	}
	if _, err := models.Platform.UpdatePlan(ctx, uuid.New(), PlanInput{Name: "x", Price: biMoney(t, "1"), BillingInterval: "monthly"}, false); !errors.Is(err, ErrRecordNotFound) {
		t.Errorf("unknown plan: err = %v", err)
	}
}
