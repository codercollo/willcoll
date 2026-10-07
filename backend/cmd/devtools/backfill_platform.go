package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/codercollo/willcoll/backend/pkg/moneyfmt"
	"github.com/google/uuid"
)

// runBackfillPlatform is backfill-platform's entry point (called from
// main() before requireDevEnv — see that file's comment). It needs its
// own pair of DSNs, both superuser/admin-role connections: SOURCE_DSN
// reads every manager's row regardless of RLS (willcoll's own
// willcoll_admin, or the migration superuser), and PLATFORM_DSN writes
// across every tenant (willcoll_platform_admin, BYPASSRLS — the app role
// couldn't insert admins/managers/plans at all, and inserting other
// managers' module_usage rows under RLS needs BYPASSRLS too).
func runBackfillPlatform(args []string) {
	sourceDSN := os.Getenv("SOURCE_DSN")
	if sourceDSN == "" {
		log.Fatal("SOURCE_DSN is not set: the old willcoll database's admin/superuser DSN to read from")
	}
	platformDSN := os.Getenv("PLATFORM_DSN")
	if platformDSN == "" {
		log.Fatal("PLATFORM_DSN is not set: willcoll_platform's willcoll_platform_admin DSN to write to")
	}

	source, platform := mustOpen(sourceDSN), mustOpen(platformDSN)
	defer func() { _ = source.Close() }()
	defer func() { _ = platform.Close() }()

	report, err := backfillPlatform(context.Background(), source, platform)
	if report != nil {
		fmt.Print(report)
	}
	if err != nil {
		log.Fatalf("backfill-platform failed partway through: %v", err)
	}
}

// backfillPlatform copies platform-owned rows from the old willcoll
// (Rentals) database into willcoll_platform (build-phases.txt Phase 3
// step 3d), and seeds module_usage and manager_modules (step 3e-b-a) for
// every existing manager. Unlike
// every other devtools subcommand, it is NOT gated by requireDevEnv
// (main.go) — it exists to run for the real cutover, in staging and
// production, not just development.
//
// Idempotent: identity rows (admins, managers, subscription_plans) are
// inserted ON CONFLICT (id) DO NOTHING, so a partial or repeated run never
// overwrites platform state a manager or admin might already have changed
// after signing in there. subscriptions and subscription_invoices UPSERT
// instead (build-phases.txt 3c-iv-b3: the newer row wins, by version /
// paid_at) — they're re-run right before cutover specifically to pick up
// whatever rentals-api wrote since the last backfill. module_usage is
// likewise always refreshed (ON CONFLICT ... DO UPDATE), since re-running
// this tool is also the easiest way to refresh a stale count by hand, and a
// stale-but-present row is exactly what module_usage is designed to
// tolerate (system-design.txt A.7).
//
// Tokens and sessions are deliberately NOT copied: they're short-lived
// (hours to days) and ambient. Every manager simply signs in again
// against platform-api after the cutover — far simpler and safer than
// preserving mid-flight activation/reset links or session cookies across
// a database that didn't exist when they were issued.
//
// Copy order respects the destination's foreign keys: admins and managers
// have none; subscription_plans references nothing; subscriptions
// references managers and subscription_plans; subscription_invoices
// references all three. Every row keeps its source id, so references
// resolve without remapping.
func backfillPlatform(ctx context.Context, source, platform *sql.DB) (*backfillReport, error) {
	report := &backfillReport{}

	if err := backfillAdmins(ctx, source, platform, report); err != nil {
		return report, fmt.Errorf("admins: %w", err)
	}
	if err := backfillManagers(ctx, source, platform, report); err != nil {
		return report, fmt.Errorf("managers: %w", err)
	}
	if err := backfillManagerModules(ctx, source, platform, report); err != nil {
		return report, fmt.Errorf("manager_modules: %w", err)
	}
	if err := backfillPlans(ctx, source, platform, report); err != nil {
		return report, fmt.Errorf("subscription_plans: %w", err)
	}
	if err := backfillSubscriptions(ctx, source, platform, report); err != nil {
		return report, fmt.Errorf("subscriptions: %w", err)
	}
	if err := backfillInvoices(ctx, source, platform, report); err != nil {
		return report, fmt.Errorf("subscription_invoices: %w", err)
	}
	if err := backfillSystemMetadata(ctx, source, platform, report); err != nil {
		return report, fmt.Errorf("system_metadata: %w", err)
	}
	if err := backfillModuleUsage(ctx, source, platform, report); err != nil {
		return report, fmt.Errorf("module_usage: %w", err)
	}
	if err := backfillPaymentChannels(ctx, source, platform, report); err != nil {
		return report, fmt.Errorf("payment_channels: %w", err)
	}

	return report, nil
}

// backfillReport counts what one run did, per table: Copied is a new row
// actually inserted, Skipped is a row that already existed at the
// destination (ON CONFLICT DO NOTHING) — expected and fine on a re-run.
type backfillReport struct {
	AdminsCopied, AdminsSkipped                 int
	ManagersCopied, ManagersSkipped             int
	ManagerModulesCopied, ManagerModulesSkipped int
	PlansCopied, PlansSkipped                   int
	SubscriptionsCopied, SubscriptionsSkipped   int
	InvoicesCopied, InvoicesSkipped             int
	MetadataCopied, MetadataSkipped             int
	// UsageRowsWritten is always an upsert, not copy-or-skip: see the
	// package doc comment.
	UsageRowsWritten                              int
	PaymentChannelsCopied, PaymentChannelsSkipped int
	// PaymentChannelDuplicates is every channel_id a property holds that
	// platform.payment_channels already has under a DIFFERENT owner — the
	// unique-ownership rule (build-phases.txt 3g-1) this backfill cannot
	// silently resolve. Each entry names the channel id and both owners;
	// surfaced so a human decides which property actually holds it.
	PaymentChannelDuplicates []string
}

func (r *backfillReport) String() string {
	s := fmt.Sprintf(
		"admins: %d copied, %d already present\n"+
			"managers: %d copied, %d already present\n"+
			"manager_modules: %d copied, %d already present\n"+
			"subscription_plans: %d copied, %d already present\n"+
			"subscriptions: %d copied, %d already present\n"+
			"subscription_invoices: %d copied, %d already present\n"+
			"system_metadata: %d copied, %d already present\n"+
			"module_usage: %d rows written (always refreshed)\n"+
			"payment_channels: %d copied, %d already present\n",
		r.AdminsCopied, r.AdminsSkipped,
		r.ManagersCopied, r.ManagersSkipped,
		r.ManagerModulesCopied, r.ManagerModulesSkipped,
		r.PlansCopied, r.PlansSkipped,
		r.SubscriptionsCopied, r.SubscriptionsSkipped,
		r.InvoicesCopied, r.InvoicesSkipped,
		r.MetadataCopied, r.MetadataSkipped,
		r.UsageRowsWritten,
		r.PaymentChannelsCopied, r.PaymentChannelsSkipped,
	)
	if len(r.PaymentChannelDuplicates) > 0 {
		s += fmt.Sprintf("payment_channels: %d DUPLICATE channel id(s), needs a human decision:\n", len(r.PaymentChannelDuplicates))
		for _, d := range r.PaymentChannelDuplicates {
			s += "  " + d + "\n"
		}
	}
	return s
}

// copiedOrSkipped classifies one ON CONFLICT DO NOTHING exec result: 1 row
// affected means it was inserted, 0 means it already existed.
func copiedOrSkipped(res sql.Result, copied, skipped *int) error {
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n > 0 {
		*copied++
	} else {
		*skipped++
	}
	return nil
}

func backfillAdmins(ctx context.Context, source, platform *sql.DB, report *backfillReport) error {
	rows, err := source.QueryContext(ctx, `SELECT id, name, email, password_hash, created_at, is_singleton FROM admins`)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var (
			id           uuid.UUID
			name, email  string
			passwordHash []byte
			createdAt    time.Time
			isSingleton  bool
		)
		if err := rows.Scan(&id, &name, &email, &passwordHash, &createdAt, &isSingleton); err != nil {
			return err
		}
		res, err := platform.ExecContext(ctx, `
			INSERT INTO admins (id, name, email, password_hash, created_at, is_singleton)
			VALUES ($1, $2, $3, $4, $5, $6)
			ON CONFLICT (id) DO NOTHING`,
			id, name, email, passwordHash, createdAt, isSingleton)
		if err != nil {
			return err
		}
		if err := copiedOrSkipped(res, &report.AdminsCopied, &report.AdminsSkipped); err != nil {
			return err
		}
	}
	return rows.Err()
}

func backfillManagers(ctx context.Context, source, platform *sql.DB, report *backfillReport) error {
	rows, err := source.QueryContext(ctx, `
		SELECT id, firm_name, username, email::text, phone, password_hash, status,
		       activated_at, trial_ends_at, created_at, version
		FROM managers`)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var (
			id                        uuid.UUID
			firmName, username, email string
			phone, status             string
			passwordHash              []byte
			activatedAt, trialEndsAt  *time.Time
			createdAt                 time.Time
			version                   int32
		)
		if err := rows.Scan(&id, &firmName, &username, &email, &phone, &passwordHash, &status,
			&activatedAt, &trialEndsAt, &createdAt, &version); err != nil {
			return err
		}
		res, err := platform.ExecContext(ctx, `
			INSERT INTO managers (id, firm_name, username, email, phone, password_hash, status,
			                       activated_at, trial_ends_at, created_at, version)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
			ON CONFLICT (id) DO NOTHING`,
			id, firmName, username, email, phone, passwordHash, status, activatedAt, trialEndsAt, createdAt, version)
		if err != nil {
			return err
		}
		if err := copiedOrSkipped(res, &report.ManagersCopied, &report.ManagersSkipped); err != nil {
			return err
		}
	}
	return rows.Err()
}

// backfillManagerModules seeds manager_modules (product='rentals') for
// every existing manager, so nobody gets locked out once requireModule
// starts gating on this table (build-phases.txt 3e-b): every manager who
// already uses Rentals is active from day one, activated_at copied from
// their own activated_at (nil for a manager who never activated, matching
// their actual account state). ON CONFLICT (manager_id, product) DO
// NOTHING, not DO UPDATE: a manager whose module status was already
// changed in platform (e.g. suspended by an admin there) must not be
// silently reset to active by a re-run of this tool.
func backfillManagerModules(ctx context.Context, source, platform *sql.DB, report *backfillReport) error {
	rows, err := source.QueryContext(ctx, `SELECT id, activated_at FROM managers`)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var (
			managerID   uuid.UUID
			activatedAt *time.Time
		)
		if err := rows.Scan(&managerID, &activatedAt); err != nil {
			return err
		}
		res, err := platform.ExecContext(ctx, `
			INSERT INTO manager_modules (manager_id, product, status, activated_at)
			VALUES ($1, 'rentals', 'active', $2)
			ON CONFLICT (manager_id, product) DO NOTHING`,
			managerID, activatedAt)
		if err != nil {
			return err
		}
		if err := copiedOrSkipped(res, &report.ManagerModulesCopied, &report.ManagerModulesSkipped); err != nil {
			return err
		}
	}
	return rows.Err()
}

// backfillPlans copies subscription_plans with product='rentals' — the
// old database has no product column, since it only ever billed one
// product. included_seats and features are left to the destination
// column defaults (1 and '{}').
func backfillPlans(ctx context.Context, source, platform *sql.DB, report *backfillReport) error {
	rows, err := source.QueryContext(ctx, `
		SELECT id, name, price, billing_interval, unit_cap, is_test, archived_at,
		       pricing_type, per_unit_price, min_price, sort_order
		FROM subscription_plans`)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var (
			id                     uuid.UUID
			name, billingInterval  string
			price                  moneyfmt.Money
			unitCap                *int32
			isTest                 bool
			archivedAt             *time.Time
			pricingType            string
			perUnitPrice, minPrice *moneyfmt.Money
			sortOrder              int32
		)
		if err := rows.Scan(&id, &name, &price, &billingInterval, &unitCap, &isTest, &archivedAt,
			&pricingType, &perUnitPrice, &minPrice, &sortOrder); err != nil {
			return err
		}
		res, err := platform.ExecContext(ctx, `
			INSERT INTO subscription_plans (id, product, name, price, billing_interval, unit_cap,
			                                 is_test, archived_at, pricing_type, per_unit_price, min_price, sort_order)
			VALUES ($1, 'rentals', $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
			ON CONFLICT (id) DO NOTHING`,
			id, name, price, billingInterval, unitCap, isTest, archivedAt, pricingType, perUnitPrice, minPrice, sortOrder)
		if err != nil {
			return err
		}
		if err := copiedOrSkipped(res, &report.PlansCopied, &report.PlansSkipped); err != nil {
			return err
		}
	}
	return rows.Err()
}

// backfillSubscriptions copies subscriptions with product='rentals'.
// Re-runnable right before cutover (build-phases.txt 3c-iv-b3): UPSERTs,
// the newer row (by version — bumped on every ActivateSubscription) winning,
// so a subscription rentals-api changed between an earlier backfill and
// cutover is picked up rather than left stale.
func backfillSubscriptions(ctx context.Context, source, platform *sql.DB, report *backfillReport) error {
	rows, err := source.QueryContext(ctx, `
		SELECT id, manager_id, plan_id, status, current_period_start, current_period_end,
		       payhero_channel_id, version
		FROM subscriptions`)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var (
			id, managerID, planID  uuid.UUID
			status                 string
			periodStart, periodEnd time.Time
			payheroChannelID       *string
			version                int32
		)
		if err := rows.Scan(&id, &managerID, &planID, &status, &periodStart, &periodEnd,
			&payheroChannelID, &version); err != nil {
			return err
		}
		res, err := platform.ExecContext(ctx, `
			INSERT INTO subscriptions (id, manager_id, product, plan_id, status,
			                            current_period_start, current_period_end, payhero_channel_id, version)
			VALUES ($1, $2, 'rentals', $3, $4, $5, $6, $7, $8)
			ON CONFLICT (id) DO UPDATE
			SET plan_id = EXCLUDED.plan_id, status = EXCLUDED.status,
			    current_period_start = EXCLUDED.current_period_start, current_period_end = EXCLUDED.current_period_end,
			    payhero_channel_id = EXCLUDED.payhero_channel_id, version = EXCLUDED.version
			WHERE EXCLUDED.version > subscriptions.version`,
			id, managerID, planID, status, periodStart, periodEnd, payheroChannelID, version)
		if err != nil {
			return err
		}
		if err := copiedOrSkipped(res, &report.SubscriptionsCopied, &report.SubscriptionsSkipped); err != nil {
			return err
		}
	}
	return rows.Err()
}

// backfillInvoices copies subscription_invoices. Re-runnable right before
// cutover (build-phases.txt 3c-iv-b3): UPSERTs, the newer row — by paid_at,
// falling back to created_at for one never paid — winning.
func backfillInvoices(ctx context.Context, source, platform *sql.DB, report *backfillReport) error {
	rows, err := source.QueryContext(ctx, `
		SELECT id, manager_id, subscription_id, plan_id, amount, period, mpesa_receipt,
		       status, paid_at, created_at
		FROM subscription_invoices`)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var (
			id, managerID, subscriptionID, planID uuid.UUID
			amount                                moneyfmt.Money
			period                                time.Time
			mpesaReceipt, status                  string
			paidAt                                *time.Time
			createdAt                             time.Time
		)
		if err := rows.Scan(&id, &managerID, &subscriptionID, &planID, &amount, &period,
			&mpesaReceipt, &status, &paidAt, &createdAt); err != nil {
			return err
		}
		res, err := platform.ExecContext(ctx, `
			INSERT INTO subscription_invoices (id, manager_id, subscription_id, plan_id, amount,
			                                    period, mpesa_receipt, status, paid_at, created_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
			ON CONFLICT (id) DO UPDATE
			SET subscription_id = EXCLUDED.subscription_id, plan_id = EXCLUDED.plan_id, amount = EXCLUDED.amount,
			    period = EXCLUDED.period, mpesa_receipt = EXCLUDED.mpesa_receipt, status = EXCLUDED.status,
			    paid_at = EXCLUDED.paid_at
			WHERE COALESCE(EXCLUDED.paid_at, EXCLUDED.created_at) > COALESCE(subscription_invoices.paid_at, subscription_invoices.created_at)`,
			id, managerID, subscriptionID, planID, amount, period, mpesaReceipt, status, paidAt, createdAt)
		if err != nil {
			return err
		}
		if err := copiedOrSkipped(res, &report.InvoicesCopied, &report.InvoicesSkipped); err != nil {
			return err
		}
	}
	return rows.Err()
}

func backfillSystemMetadata(ctx context.Context, source, platform *sql.DB, report *backfillReport) error {
	rows, err := source.QueryContext(ctx, `SELECT key, value, updated_at FROM system_metadata`)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var (
			key       string
			value     []byte
			updatedAt time.Time
		)
		if err := rows.Scan(&key, &value, &updatedAt); err != nil {
			return err
		}
		res, err := platform.ExecContext(ctx, `
			INSERT INTO system_metadata (key, value, updated_at)
			VALUES ($1, $2, $3)
			ON CONFLICT (key) DO NOTHING`,
			key, value, updatedAt)
		if err != nil {
			return err
		}
		if err := copiedOrSkipped(res, &report.MetadataCopied, &report.MetadataSkipped); err != nil {
			return err
		}
	}
	return rows.Err()
}

// backfillModuleUsage seeds module_usage for every existing manager,
// product='rentals', using the exact same query as billing.go's
// CountManagerUnits: occupied+vacant units across the manager's live
// (non-archived) properties. Always upserts, unlike the identity/billing
// tables above — see the package doc comment.
func backfillModuleUsage(ctx context.Context, source, platform *sql.DB, report *backfillReport) error {
	rows, err := source.QueryContext(ctx, `
		SELECT m.id, (
			SELECT count(*)::int FROM units u
			JOIN properties p ON p.id = u.property_id
			WHERE u.tenant_id = m.id AND p.deleted_at IS NULL
		) AS unit_count
		FROM managers m`)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()

	type usage struct {
		managerID uuid.UUID
		count     int32
	}
	var counts []usage
	for rows.Next() {
		var u usage
		if err := rows.Scan(&u.managerID, &u.count); err != nil {
			return err
		}
		counts = append(counts, u)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	for _, u := range counts {
		_, err := platform.ExecContext(ctx, `
			INSERT INTO module_usage (manager_id, product, billable_count, reported_at)
			VALUES ($1, 'rentals', $2, now())
			ON CONFLICT (manager_id, product) DO UPDATE
			SET billable_count = EXCLUDED.billable_count, reported_at = EXCLUDED.reported_at`,
			u.managerID, u.count)
		if err != nil {
			return err
		}
		report.UsageRowsWritten++
	}
	return nil
}

// backfillPaymentChannels registers every property's existing
// payhero_channel_id in platform.payment_channels (build-phases.txt 3g-1):
// product='rentals', owner_id=the property's own id, manager_id=the
// property's tenant. Since 3i-d new hand-typed channels are claimed by
// rentals-api itself at save time; this pass is the catch-up for rows saved
// before that (and any claim lost since), and the place conflicts are
// reported. It needs the willcoll_platform OWNER DSN as PLATFORM_DSN.
// Idempotent on (channel_id already present for this
// exact owner) — ON CONFLICT (channel_id) DO NOTHING, then a follow-up read
// tells apart "already registered to this same property" (skipped,
// expected on a re-run) from "registered to a DIFFERENT owner" (a real
// duplicate the backfill cannot resolve by itself, surfaced in the report
// rather than silently picked a winner). callback_url is the one rentals
// webhook every channel posts to, built from PAYHERO_CALLBACK_BASE_URL —
// the same env var rentals-api itself uses (cmd/rentals-api/pay.go's
// callbackURL) — since this tool has no running app to ask.
func backfillPaymentChannels(ctx context.Context, source, platform *sql.DB, report *backfillReport) error {
	base := strings.TrimRight(strings.TrimSpace(os.Getenv("PAYHERO_CALLBACK_BASE_URL")), "/")
	if base == "" {
		log.Println("payment_channels: PAYHERO_CALLBACK_BASE_URL not set; skipping (set it to register existing channels)")
		return nil
	}
	callbackURL := base + "/v1/webhooks/payhero/collections"

	rows, err := source.QueryContext(ctx, `
		SELECT id, tenant_id, payhero_channel_id FROM properties
		WHERE payhero_channel_id IS NOT NULL AND payhero_channel_id != ''`)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()

	type channel struct {
		propertyID, managerID uuid.UUID
		channelID             string
	}
	var channels []channel
	for rows.Next() {
		var c channel
		if err := rows.Scan(&c.propertyID, &c.managerID, &c.channelID); err != nil {
			return err
		}
		channels = append(channels, c)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	for _, c := range channels {
		res, err := platform.ExecContext(ctx, `
			INSERT INTO payment_channels (channel_id, manager_id, product, owner_id, callback_url)
			VALUES ($1, $2, 'rentals', $3, $4)
			ON CONFLICT (channel_id) DO NOTHING`,
			c.channelID, c.managerID, c.propertyID, callbackURL)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n > 0 {
			report.PaymentChannelsCopied++
			continue
		}
		report.PaymentChannelsSkipped++

		// owner_id is nullable (a claim with no owner, e.g. a platform
		// channel), so it scans as a NullString; a claim under another
		// product is a conflict even if the owner id happened to match.
		var existingProduct string
		var existingOwner sql.NullString
		if err := platform.QueryRowContext(ctx,
			`SELECT product, owner_id::text FROM payment_channels WHERE channel_id = $1`, c.channelID,
		).Scan(&existingProduct, &existingOwner); err != nil {
			return err
		}
		if existingProduct != "rentals" || !existingOwner.Valid || existingOwner.String != c.propertyID.String() {
			report.PaymentChannelDuplicates = append(report.PaymentChannelDuplicates,
				fmt.Sprintf("channel %s: property %s wants it, already registered to %s owner %s",
					c.channelID, c.propertyID, existingProduct, existingOwner.String))
		}
	}
	return nil
}
