package branding

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

func TestBrandVars(t *testing.T) {
	ctx := context.Background()
	s := NewService(testPool)

	orgID := uuid.New()
	slug := "brandvars-" + uuid.NewString()

	if _, err := testPool.Exec(ctx, `
		INSERT INTO organizations (id, name, brand_name, slug, logo_url)
		VALUES ($1, $2, $3, $4, $5)`,
		orgID, "Legal Name", "Rentman", slug, "/logos/rentman.png",
	); err != nil {
		t.Fatalf("insert organization: %v", err)
	}

	v, err := s.BrandVars(ctx, orgID)
	if err != nil {
		t.Fatalf("BrandVars: %v", err)
	}
	if v.BrandName != "Rentman" {
		t.Fatalf("BrandName = %q, want %q", v.BrandName, "Rentman")
	}
	if v.LogoURL != "/logos/rentman.png" {
		t.Fatalf("LogoURL = %q, want %q", v.LogoURL, "/logos/rentman.png")
	}
}
