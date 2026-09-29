// Command devtools provides local-only helpers for exercising the trial and
// subscription paywall (internal/data/access.go, cmd/api/access.go) without
// waiting on PayHero or the clock. It refuses to run unless ENV is exactly
// "development": these subcommands write a fake, worthless plan and rewrite
// a real manager's trial/subscription dates, which must never touch staging
// or production.
//
// Usage (via the Makefile, which loads backend/.env first):
//
//	make dev/test-plan
//	make dev/expire-trial EMAIL=demo@willcoll.test
//	make dev/reset-trial  EMAIL=demo@willcoll.test
//	make dev/set-plan     EMAIL=demo@willcoll.test PLAN="TEST - KES 10"
package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/url"
	"os"
	"time"

	"github.com/codercollo/willcoll/backend/internal/data"
	"github.com/codercollo/willcoll/backend/pkg/moneyfmt"
	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// TestPlanName is the fake plan dev/test-plan upserts: cheap enough to pay
// for real over M-Pesa while poking the renew flow, never a plan a real
// firm should see.
const TestPlanName = "TEST - KES 10"

func main() {
	requireDevEnv()

	if len(os.Args) < 2 {
		log.Fatal("usage: devtools <test-plan|expire-trial|reset-trial> [flags]")
	}
	cmd, args := os.Args[1], os.Args[2:]

	appDSN := os.Getenv("DB_DSN")
	if appDSN == "" {
		log.Fatal("DB_DSN is not set (run through `make dev/...`, which loads backend/.env)")
	}
	adminDSN := os.Getenv("DB_ADMIN_DSN")
	if adminDSN == "" {
		adminDSN = deriveAdminDSN(appDSN, os.Getenv("DB_ADMIN_PASSWORD"))
	}
	if adminDSN == "" {
		log.Fatal("set DB_ADMIN_DSN, or DB_ADMIN_PASSWORD to derive it from DB_DSN")
	}

	app, admin := mustOpen(appDSN), mustOpen(adminDSN)
	defer app.Close()
	defer admin.Close()
	models := data.NewModels(app, admin, 30*time.Second)
	ctx := context.Background()

	switch cmd {
	case "test-plan":
		testPlan(ctx, models)
	case "expire-trial":
		email := emailFlag(args)
		expireTrial(ctx, models, email)
	case "reset-trial":
		email := emailFlag(args)
		resetTrial(ctx, models, email)
	case "set-plan":
		email, plan := emailAndPlanFlags(args)
		setPlan(ctx, models, email, plan)
	default:
		log.Fatalf("unknown devtools command %q", cmd)
	}
}

// requireDevEnv is the guard every subcommand runs before touching the
// database: these tools upsert a fake plan and rewrite trial/subscription
// dates, which must never run against staging or production.
func requireDevEnv() {
	if err := checkDevEnv(os.Getenv("ENV")); err != nil {
		log.Fatal(err)
	}
}

// checkDevEnv is requireDevEnv's logic, isolated so it's unit-testable
// without a database or a fatal exit.
func checkDevEnv(env string) error {
	if env != "development" {
		return fmt.Errorf("devtools refuses to run with ENV=%q: must be exactly \"development\"", env)
	}
	return nil
}

func emailFlag(args []string) string {
	fs := flag.NewFlagSet("devtools", flag.ExitOnError)
	email := fs.String("email", "", "manager email")
	_ = fs.Parse(args)
	if *email == "" {
		log.Fatal("usage: devtools expire-trial|reset-trial -email=<manager email>")
	}
	return *email
}

func emailAndPlanFlags(args []string) (string, string) {
	fs := flag.NewFlagSet("devtools", flag.ExitOnError)
	email := fs.String("email", "", "manager email")
	plan := fs.String("plan", "", "plan name, exactly as listed by GET /v1/billing/plans")
	_ = fs.Parse(args)
	if *email == "" || *plan == "" {
		log.Fatal("usage: devtools set-plan -email=<manager email> -plan=<plan name>")
	}
	return *email, *plan
}

// testPlan upserts TestPlanName: KES 10, monthly, no unit cap. It is
// idempotent so `make dev/test-plan` is safe to re-run.
func testPlan(ctx context.Context, models data.Models) {
	price, err := moneyfmt.Parse("10.00")
	if err != nil {
		log.Fatal(err)
	}
	input := data.PlanInput{Name: TestPlanName, Price: price, BillingInterval: "monthly", IsTest: true, SortOrder: 999}

	plans, err := models.Platform.ListPlans(ctx)
	if err != nil {
		log.Fatal(err)
	}
	for _, p := range plans {
		if p.Name == TestPlanName {
			// A dev-only tool: always run as if in development, since it
			// only exists to seed the TEST plan for local use.
			if _, err := models.Platform.UpdatePlan(ctx, p.ID, input, true); err != nil {
				log.Fatal(err)
			}
			fmt.Printf("updated plan %s: %s\n", p.ID, TestPlanName)
			return
		}
	}
	p, err := models.Platform.CreatePlan(ctx, input, true)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("created plan %s: %s\n", p.ID, TestPlanName)
}

// expireTrial sets the manager's trial end, and any current subscription
// period end, to yesterday: the 402 paywall should now close the dashboard.
func expireTrial(ctx context.Context, models data.Models, email string) {
	manager, err := models.Managers.GetByEmail(ctx, email)
	if err != nil {
		log.Fatalf("manager %s: %v", email, err)
	}

	yesterday := time.Now().AddDate(0, 0, -1)
	manager.TrialEndsAt = &yesterday
	if err := models.Managers.Update(ctx, manager); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("trial_ends_at set to %s for %s\n", yesterday.Format(time.RFC3339), email)

	sub, err := models.Billing.Current(ctx, manager.ID)
	if errors.Is(err, data.ErrRecordNotFound) {
		fmt.Println("no subscription to expire")
		return
	}
	if err != nil {
		log.Fatal(err)
	}
	if _, err := models.Billing.ExpireForDev(ctx, manager.ID, sub.Subscription.ID, today(yesterday)); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("subscription %s current_period_end set to %s\n", sub.Subscription.ID, today(yesterday).Format("2006-01-02"))
}

// resetTrial restores a fresh 7-day trial and deletes any subscription the
// dev tools created, so the manager is back to a brand-new trial firm.
func resetTrial(ctx context.Context, models data.Models, email string) {
	manager, err := models.Managers.GetByEmail(ctx, email)
	if err != nil {
		log.Fatalf("manager %s: %v", email, err)
	}

	trialEnd := time.Now().AddDate(0, 0, data.DefaultTrialDays)
	manager.TrialEndsAt = &trialEnd
	if err := models.Managers.Update(ctx, manager); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("trial_ends_at reset to %s for %s\n", trialEnd.Format(time.RFC3339), email)

	n, err := models.Billing.DeleteForDev(ctx, manager.ID)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("removed %d subscription(s)\n", n)
}

// setPlan puts a manager directly onto plan, active for one billing interval
// from today, with no invoice or payment: for exercising the renew flow's
// plan-switch behavior without a real PayHero payment.
func setPlan(ctx context.Context, models data.Models, email, plan string) {
	manager, err := models.Managers.GetByEmail(ctx, email)
	if err != nil {
		log.Fatalf("manager %s: %v", email, err)
	}

	plans, err := models.Platform.ListPlans(ctx)
	if err != nil {
		log.Fatal(err)
	}
	var planID *uuid.UUID
	for _, p := range plans {
		if p.Name == plan {
			id := p.ID
			planID = &id
			break
		}
	}
	if planID == nil {
		log.Fatalf("no plan named %q (see GET /v1/billing/plans)", plan)
	}

	if err := models.Billing.SetPlanForDev(ctx, manager.ID, *planID); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("%s is now on plan %q\n", email, plan)
}

func today(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

func mustOpen(dsn string) *sql.DB {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		log.Fatal(err)
	}
	if err := db.Ping(); err != nil {
		log.Fatalf("cannot reach the database: %v", err)
	}
	return db
}

func deriveAdminDSN(appDSN, adminPassword string) string {
	if adminPassword == "" {
		return ""
	}
	u, err := url.Parse(appDSN)
	if err != nil || u.Scheme == "" {
		return ""
	}
	u.User = url.UserPassword("willcoll_admin", adminPassword)
	return u.String()
}
