// Command willcoll-seed is the standalone demo-portfolio seeder, kept as a
// separate binary so `make seed` never ships in the production image
// (project-structure.txt §cmd). Seeds exactly one Organization + one
// activated Manager with a known dev password, so a fresh `make reset-dev`
// gives a one-command login locally — no activation email needed (spec
// Phase 6.1). Idempotent: re-running it after the user already exists is a
// no-op that just reprints the same credentials, not a unique-violation error.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"

	"github.com/codercollo/willcoll-sys/internal/config"
	"github.com/codercollo/willcoll-sys/internal/db"
	"github.com/codercollo/willcoll-sys/internal/tenancy"
	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/bcrypt"
)

const (
	devOrgName  = "Willcoll Dev"
	devFullName = "Dev Manager"
	devPhone    = "+254700000000"
	devEmail    = "manager@willcoll.dev"
	devPassword = "willcoll-dev"
	devTier     = "professional"
	devBilling  = "active"
)

func main() {
	if err := run(); err != nil {
		log.Fatalf("seed: %v", err)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	ctx := context.Background()
	pool, err := db.NewPool(ctx, cfg.DatabaseDSN)
	if err != nil {
		return fmt.Errorf("open db pool: %w", err)
	}
	defer pool.Close()

	var existingSlug string
	err = pool.QueryRow(ctx, `
		SELECT o.slug
		FROM users u
		JOIN organizations o ON o.id = u.organization_id
		WHERE u.email = $1`,
		devEmail,
	).Scan(&existingSlug)
	if err == nil {
		fmt.Println("seed: dev manager already exists, nothing to do")
		printCredentials(existingSlug)
		return nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("check existing dev manager: %w", err)
	}

	passwordHash, err := bcrypt.GenerateFromPassword([]byte(devPassword), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("hash dev password: %w", err)
	}

	tenancyService := tenancy.NewService(pool)
	org, user, err := tenancyService.CreateOrganizationWithFirstManagerTx(ctx, tenancy.CreateOrganizationInput{
		Name:             devOrgName,
		SubscriptionTier: devTier,
		BillingStatus:    devBilling,
		FullName:         devFullName,
		Phone:            devPhone,
		Email:            devEmail,
		PasswordHash:     string(passwordHash),
	})
	if err != nil {
		return fmt.Errorf("create dev organization/manager: %w", err)
	}

	fmt.Printf("seed: created organization %q and manager %s\n", org.Name, user.Email)
	printCredentials(org.Slug)
	return nil
}

func printCredentials(slug string) {
	fmt.Println("seed: dev login —")
	fmt.Printf("  slug:     %s\n", slug)
	fmt.Printf("  email:    %s\n", devEmail)
	fmt.Printf("  password: %s\n", devPassword)
}
