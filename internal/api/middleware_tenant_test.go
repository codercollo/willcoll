package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/codercollo/willcoll-sys/internal/auth"
	"github.com/codercollo/willcoll-sys/internal/db"
	"github.com/codercollo/willcoll-sys/internal/tenancy"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
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

// TestTenantScopeReliesOnRLSToBlockCrossOrganizationReads proves the tenancy
// middleware is not the real isolation backstop: even when a handler targets
// another Organization's row directly, RLS (scoped to the token's own
// organization_id) returns no rows.
func TestTenantScopeReliesOnRLSToBlockCrossOrganizationReads(t *testing.T) {
	ctx := context.Background()

	tenancyService := tenancy.NewService(testPool)
	authService := auth.NewService([]byte("01234567890123456789012345678901"), testPool)

	orgA, userA, err := tenancyService.CreateOrganizationWithFirstManagerTx(ctx, tenancy.CreateOrganizationInput{
		Name:         "org-a-" + uuid.NewString(),
		FullName:     "Manager A",
		Phone:        uuid.NewString(),
		Email:        "a-" + uuid.NewString() + "@example.com",
		PasswordHash: "unused",
	})
	if err != nil {
		t.Fatalf("create org A: %v", err)
	}

	_, userB, err := tenancyService.CreateOrganizationWithFirstManagerTx(ctx, tenancy.CreateOrganizationInput{
		Name:         "org-b-" + uuid.NewString(),
		FullName:     "Manager B",
		Phone:        uuid.NewString(),
		Email:        "b-" + uuid.NewString() + "@example.com",
		PasswordHash: "unused",
	})
	if err != nil {
		t.Fatalf("create org B: %v", err)
	}

	token, err := authService.IssueToken(userA.ID, "manager", orgA.ID)
	if err != nil {
		t.Fatalf("issue token: %v", err)
	}

	srv := NewServer(testPool, authService, tenancyService, nil, nil, nil, nil, nil, nil, nil)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tx, ok := requestTxFromContext(r.Context())
		if !ok {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		var fullName string
		err := tx.QueryRow(r.Context(), `SELECT full_name FROM users WHERE id = $1`, userB.ID).Scan(&fullName)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			// RLS held: the caller's transaction is scoped to org A, so the
			// org B user is invisible.
			w.WriteHeader(http.StatusOK)
		case err != nil:
			w.WriteHeader(http.StatusInternalServerError)
		default:
			// RLS failed open — this test exists to catch exactly this.
			w.WriteHeader(http.StatusInternalServerError)
		}
	})

	req := httptest.NewRequest(http.MethodGet, "http://example/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()

	srv.authenticate(srv.tenantScope(handler)).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: the foreign organization's user should be invisible under RLS", rr.Code, http.StatusOK)
	}
}
