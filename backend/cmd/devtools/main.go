// Command devtools provides local-only helpers for exercising the trial and
// subscription paywall (internal/platform/data/access.go,
// cmd/rentals-api/gates.go) without waiting on PayHero or the clock. Those
// subcommands refuse to run unless ENV is exactly "development": they
// write a fake, worthless plan and rewrite a real manager's trial/
// subscription dates, which must never touch staging or production.
//
// backfill-platform is the one exception (build-phases.txt Phase 3 step
// 3d): it copies platform-owned rows into willcoll_platform for the real
// cutover, so it is NOT gated by ENV — see backfill_platform.go.
//
// Since build-phases.txt 3i-b every other subcommand reads and writes
// willcoll_platform only (identity, plans, trials and subscriptions live
// there; rentals' own database has none of it any more). They open
// PLATFORM_DSN — willcoll_platform's willcoll_platform_admin DSN, the same
// one backfill-platform uses — and operate on the "rentals" product.
//
// Usage (via the Makefile, which loads backend/.env first):
//
//	make dev/test-plan
//	make dev/expire-trial EMAIL=demo@willcoll.test
//	make dev/reset-trial  EMAIL=demo@willcoll.test
//	make dev/set-plan     EMAIL=demo@willcoll.test PLAN="TEST - KES 10"
//	make db/backfill-platform
//
// claim-channels (run by make dev/setup) claims and verifies every
// property's payment channel in platform; see claim_channels.go.
package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	platformdata "github.com/codercollo/willcoll/backend/internal/platform/data"
	platformdb "github.com/codercollo/willcoll/backend/internal/platform/db"
	"github.com/codercollo/willcoll/backend/pkg/moneyfmt"
	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// TestPlanName is the fake plan dev/test-plan upserts: cheap enough to pay
// for real over M-Pesa while poking the renew flow, never a plan a real
// firm should see.
const TestPlanName = "TEST - KES 10"

func main() {
	if len(os.Args) < 2 {
		log.Fatal("usage: devtools <test-plan|expire-trial|reset-trial|set-plan|claim-channels|backfill-platform> [flags]")
	}
	cmd, args := os.Args[1], os.Args[2:]

	// backfill-platform is the one subcommand that runs outside
	// development (build-phases.txt 3d) and needs a different pair of
	// DSNs (source willcoll, destination willcoll_platform), so it's
	// dispatched before requireDevEnv and before the rest of main() opens
	// the usual DB_DSN/DB_ADMIN_DSN pair.
	if cmd == "backfill-platform" {
		runBackfillPlatform(args)
		return
	}

	requireDevEnv()

	switch cmd {
	case "test-plan":
		runTestPlan()
	case "expire-trial":
		runExpireTrial(emailFlag(args))
	case "reset-trial":
		runResetTrial(emailFlag(args))
	case "claim-channels":
		runClaimChannels()
	case "set-plan":
		email, plan := emailAndPlanFlags(args)
		runSetPlan(email, plan)
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

// openPlatform opens willcoll_platform as the admin role (PLATFORM_DSN, the
// same variable backfill-platform uses) and returns its models, with the
// dev-only methods unlocked: requireDevEnv has already checked ENV, and
// each dev method checks it again itself.
func openPlatform() (platformdata.Models, func()) {
	platformDSN := os.Getenv("PLATFORM_DSN")
	if platformDSN == "" {
		log.Fatal("PLATFORM_DSN is not set: willcoll_platform's willcoll_platform_admin DSN (same var backfill-platform uses)")
	}
	platformDB := mustOpen(platformDSN)

	models := platformdata.NewModelsFromStore(platformdb.NewStore(platformDB), 30*time.Second)
	models.Billing.Env = "development"
	models.Modules.Env = "development"
	return models, func() { _ = platformDB.Close() }
}

// runTestPlan upserts TestPlanName into the platform catalog: KES 10,
// monthly, rentals, no unit cap. It is idempotent so `make dev/test-plan`
// is safe to re-run.
func runTestPlan() {
	models, closeDB := openPlatform()
	defer closeDB()
	ctx := context.Background()

	price, err := moneyfmt.Parse("10.00")
	if err != nil {
		log.Fatal(err)
	}
	input := platformdata.PlanInput{
		Product: platformdata.ProductRentals, Name: TestPlanName, Price: price,
		BillingInterval: "monthly", IncludedSeats: 1, Features: []string{},
		IsTest: true, PricingType: platformdata.PricingFlat, SortOrder: 999,
	}

	plans, err := models.Plans.List(ctx)
	if err != nil {
		log.Fatal(err)
	}
	for _, p := range plans {
		if p.Product == platformdata.ProductRentals && p.Name == TestPlanName && !p.Archived {
			// A dev-only tool: always run as if in development, since it
			// only exists to seed the TEST plan for local use.
			if _, err := models.Plans.Update(ctx, p.ID, input, true); err != nil {
				log.Fatal(err)
			}
			fmt.Printf("updated plan %s: %s\n", p.ID, TestPlanName)
			return
		}
	}
	p, err := models.Plans.Create(ctx, input, true)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("created plan %s: %s\n", p.ID, TestPlanName)
}

// runExpireTrial sets the manager's Rentals trial end, and any current
// Rentals subscription period end, to yesterday: the 402 paywall should
// now close the dashboard.
func runExpireTrial(email string) {
	models, closeDB := openPlatform()
	defer closeDB()
	ctx := context.Background()

	manager, err := models.Managers.GetByEmail(ctx, email)
	if err != nil {
		log.Fatalf("manager %s: %v", email, err)
	}

	yesterday := moneyfmt.Now().AddDate(0, 0, -1)
	if err := models.Modules.SetTrialEndForDev(ctx, manager.ID, platformdata.ProductRentals, yesterday); err != nil {
		log.Fatalf("setting the rentals trial end (does %s have a rentals module?): %v", email, err)
	}
	fmt.Printf("rentals trial_ends_at set to %s for %s\n", yesterday.Format(time.RFC3339), email)

	_, err = models.Billing.ExpireForDev(ctx, manager.ID, platformdata.ProductRentals, today(yesterday))
	if errors.Is(err, platformdata.ErrRecordNotFound) {
		fmt.Println("no subscription to expire")
		return
	}
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("rentals subscription current_period_end set to %s\n", today(yesterday).Format("2006-01-02"))
}

// runResetTrial restores a fresh trial and deletes any Rentals subscription
// the dev tools created, so the manager is back to a brand-new trial firm.
func runResetTrial(email string) {
	models, closeDB := openPlatform()
	defer closeDB()
	ctx := context.Background()

	manager, err := models.Managers.GetByEmail(ctx, email)
	if err != nil {
		log.Fatalf("manager %s: %v", email, err)
	}

	trialEnd := platformdata.TrialEnd(moneyfmt.Now(), platformdata.DefaultTrialDays)
	if err := models.Modules.SetTrialEndForDev(ctx, manager.ID, platformdata.ProductRentals, trialEnd); err != nil {
		log.Fatalf("setting the rentals trial end (does %s have a rentals module?): %v", email, err)
	}
	fmt.Printf("rentals trial_ends_at reset to %s for %s\n", trialEnd.Format(time.RFC3339), email)

	n, err := models.Billing.DeleteForDev(ctx, manager.ID, platformdata.ProductRentals)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("removed %d subscription(s)\n", n)
}

// runSetPlan puts email's manager directly onto plan for the rentals
// product, active for one billing interval from today, with no invoice or
// payment: for exercising the renew flow's plan-switch behavior without a
// real PayHero payment. plan must exist in the platform catalog
// (dev/test-plan, or POST /v1/admin/plans against platform-api).
func runSetPlan(email, plan string) {
	models, closeDB := openPlatform()
	defer closeDB()
	ctx := context.Background()

	manager, err := models.Managers.GetByEmail(ctx, email)
	if err != nil {
		log.Fatalf("manager %s: %v", email, err)
	}

	plans, err := models.Plans.List(ctx)
	if err != nil {
		log.Fatal(err)
	}
	var planID *uuid.UUID
	for _, p := range plans {
		if p.Product == platformdata.ProductRentals && p.Name == plan && !p.Archived {
			id := p.ID
			planID = &id
			break
		}
	}
	if planID == nil {
		log.Fatalf("no rentals plan named %q in the platform catalog (see GET /v1/admin/plans against platform-api)", plan)
	}

	if err := models.Billing.SetPlanForDev(ctx, manager.ID, platformdata.ProductRentals, *planID); err != nil {
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
