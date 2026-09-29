// Package data is the service layer, in the shape of Greenlight's Models
// aggregate: handlers call app.models.Properties.Insert(...) and never see
// SQL. Each model holds the business rules for one aggregate and runs its
// queries through db.Store — the sqlc repository layer — inside the
// tenant's RLS-scoped transaction (system-design.txt 3.7).
//
// # Data typing rules
//
// These are enforced for the schema by TestSchemaTypingRules, which reads
// every migration.
//
// Identifiers are uuid, never bigserial or any auto-increment integer.
// Willcoll is multi-tenant (system-design.txt 3.7): a predictable, sequential
// ID that appears in a URL or a response would let one manager infer another
// tenant's row counts, and would make guessing at other rows easy. The only
// primary keys that are not uuid are internal and never leave the server: the
// SHA-256 hash of a token (tokens.hash), an SCS session token
// (sessions.token) and the key of system_metadata. Receipt numbers
// (receipts.receipt_no) are sequential per manager on purpose, because the
// paper receipt book is, but they are a business number, never a key.
//
// Money is numeric(12,2) from Postgres to JSON: the column, then
// moneyfmt.Money in Go (whole cents in an int64), then a decimal string in
// JSON ("1900.50"). Never float64, and never a bare integer-cents column.
// Meter readings share the type, so a reading is exact too. Amounts are
// parsed from their literal text, never through a float. A percentage such as
// management_fee_percent is numeric(5,2) and is the one non-money decimal;
// applying it to money goes through Money.PercentOf, in integer arithmetic.
//
// Times are timestamptz; a calendar date (a billing period, a lease start) is
// date. Payment dates shown to people are Kenya dates (Africa/Nairobi), not
// UTC.
package data

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/codercollo/willcoll/backend/internal/db"
)

var (
	// ErrRecordNotFound is returned when a query expected exactly one row
	// (Get) and found none — including rows that exist but belong to
	// another tenant, which RLS hides.
	ErrRecordNotFound = errors.New("record not found")

	// ErrEditConflict is returned by an Update when the row's version no
	// longer matches the version the caller read (optimistic concurrency
	// control, Greenlight ch.8.2).
	ErrEditConflict = errors.New("edit conflict")
)

// Postgres error codes this package translates into sentinel errors.
const (
	pgUniqueViolation = "23505"
)

// DefaultQueryTimeout bounds every model method when no timeout is
// configured, so a stuck query can't hold a pooled connection forever
// (Greenlight ch.8.3). Configured via DB_QUERY_TIMEOUT.
const DefaultQueryTimeout = 3 * time.Second

// Models is the single aggregate handlers depend on.
type Models struct {
	Admins      AdminModel
	Platform    PlatformModel
	Managers    ManagerModel
	Landlords   LandlordModel
	Properties  PropertyModel
	Units       UnitModel
	Leases      LeaseModel
	Ledger      LedgerModel
	Payments    PaymentModel
	Tokens      TokenModel
	Water       WaterModel
	Garbage     GarbageModel
	Electricity ElectricityModel
	Reports     ReportModel
	Receipts    ReceiptModel
	Rent        RentModel
	Resolver    ResolverModel
	Collections CollectionsModel
	Billing     BillingModel
	PayAccess   PayAccessModel
	UnitQR      UnitQRModel
	Onboarding  OnboardingModel
}

// NewModels wires the models to their connection pools: conn is the API's
// willcoll_app pool (subject to RLS); adminConn is the willcoll_admin pool
// (BYPASSRLS), used only by Platform for /v1/admin/* (system-design.txt
// 3.7). Each method call is bounded by timeout (zero means
// DefaultQueryTimeout).
func NewModels(conn, adminConn *sql.DB, timeout time.Duration) Models {
	return newModels(db.NewStore(conn), db.NewStore(adminConn), timeout)
}

// NewModelsFromStore wires every model, Platform included, to one store;
// tests pass a mock.
func NewModelsFromStore(store db.Store, timeout time.Duration) Models {
	return newModels(store, store, timeout)
}

func newModels(store, adminStore db.Store, timeout time.Duration) Models {
	// The only cross-tenant reads: id-only lookups on the BYPASSRLS pool.
	resolver := ResolverModel{Store: adminStore, Timeout: timeout}
	return Models{
		Admins:      AdminModel{Store: store, Timeout: timeout},
		Platform:    PlatformModel{Store: adminStore, Timeout: timeout},
		Managers:    ManagerModel{Store: store, Timeout: timeout},
		Landlords:   LandlordModel{Store: store, Timeout: timeout},
		Properties:  PropertyModel{Store: store, Timeout: timeout},
		Units:       UnitModel{Store: store, Timeout: timeout},
		Leases:      LeaseModel{Store: store, Timeout: timeout},
		Ledger:      LedgerModel{Store: store, Timeout: timeout},
		Payments:    PaymentModel{Store: store, Timeout: timeout},
		Tokens:      TokenModel{Store: store, Timeout: timeout},
		Water:       WaterModel{Store: store, Timeout: timeout},
		Garbage:     GarbageModel{Store: store, Timeout: timeout},
		Electricity: ElectricityModel{Store: store, Timeout: timeout},
		Reports:     ReportModel{Store: store, Timeout: timeout},
		Receipts:    ReceiptModel{Store: store, Timeout: timeout},
		Rent:        RentModel{Store: store, Timeout: timeout},
		Resolver:    resolver,
		Collections: CollectionsModel{Store: store, Resolver: resolver, Timeout: timeout},
		Billing:     BillingModel{Store: store, Resolver: resolver, Timeout: timeout},
		PayAccess:   PayAccessModel{Store: store, Timeout: timeout},
		UnitQR:      UnitQRModel{Store: store, Resolver: adminStore, Timeout: timeout},
		Onboarding:  OnboardingModel{Store: store, Timeout: timeout},
	}
}

// notFound maps sql.ErrNoRows from a single-row query to ErrRecordNotFound.
func notFound(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrRecordNotFound
	}
	return err
}

// editConflict maps sql.ErrNoRows from a version-checked UPDATE ... RETURNING
// to ErrEditConflict: either the version moved on, or the row is gone.
func editConflict(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrEditConflict
	}
	return err
}

// withTimeout bounds ctx by d, or DefaultQueryTimeout when d is zero.
func withTimeout(ctx context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	if d <= 0 {
		d = DefaultQueryTimeout
	}
	return context.WithTimeout(ctx, d)
}
