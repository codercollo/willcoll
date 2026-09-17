package tenancy

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/codercollo/willcoll-sys/internal/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

var testPool *pgxpool.Pool

func TestMain(m *testing.M) {
	dsn := os.Getenv("DATABASE_DSN")
	if dsn == "" {
		dsn = "postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable"
	}

	if err := db.RunMigrations(dsn); err != nil {
		fmt.Fprintf(os.Stderr, "run migrations: %v\n", err)
		os.Exit(1)
	}

	pool, err := db.NewPool(context.Background(), dsn)
	if err != nil {
		fmt.Fprintf(os.Stderr, "open pool: %v\n", err)
		os.Exit(1)
	}
	testPool = pool

	code := m.Run()

	pool.Close()
	os.Exit(code)
}

func TestCreateOrganizationWithFirstManagerTxRollsBackOrganizationOnUserInsertFailure(t *testing.T) {
	ctx := context.Background()
	s := NewService(testPool)

	// phone is TEXT in Phase 1 (MSISDN validation is a later-phase handler
	// concern); uniqueness is all this test needs, so UUIDs keep re-runs clean.
	duplicateEmail := "dup-" + uuid.NewString() + "@example.com"
	seedPhone := uuid.NewString()
	managerPhone := uuid.NewString()

	// Seed a conflicting user in a separate organization so the first-manager
	// insert below trips users.email's UNIQUE constraint.
	tx, err := testPool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin seed tx: %v", err)
	}
	defer tx.Rollback(ctx)

	seedOrgID := uuid.New()
	seedSlug := "seed-" + uuid.NewString()
	if _, err := tx.Exec(ctx,
		`INSERT INTO organizations (id, name, slug, brand_name) VALUES ($1, $2, $3, $2)`,
		seedOrgID, seedSlug, seedSlug,
	); err != nil {
		t.Fatalf("insert seed org: %v", err)
	}
	if err := s.Scope(ctx, tx, seedOrgID); err != nil {
		t.Fatalf("scope seed tx: %v", err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO users (id, organization_id, full_name, phone, email, password_hash, role, activated, status)
		 VALUES ($1, $2, $3, $4, $5, $6, 'manager', true, 'active')`,
		uuid.New(), seedOrgID, "Seed Manager", seedPhone, duplicateEmail, "unused",
	); err != nil {
		t.Fatalf("insert seed user: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit seed tx: %v", err)
	}

	orgName := "org-b-" + uuid.NewString()

	_, _, err = s.CreateOrganizationWithFirstManagerTx(ctx, CreateOrganizationInput{
		Name:         orgName,
		FullName:     "Manager B",
		Phone:        managerPhone,
		Email:        duplicateEmail,
		PasswordHash: "irrelevant-for-this-test",
	})
	if err == nil {
		t.Fatal("expected first-manager insert to fail on duplicate email")
	}

	var exists bool
	if err := testPool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM organizations WHERE name = $1)`,
		orgName,
	).Scan(&exists); err != nil {
		t.Fatalf("check rolled-back organization: %v", err)
	}
	if exists {
		t.Fatalf("organization %q was not rolled back after user insert failed", orgName)
	}
}
