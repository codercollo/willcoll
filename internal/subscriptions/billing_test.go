package subscriptions

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
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

func TestIntasendWebhookSameReferenceTransitionsOnce(t *testing.T) {
	ctx := context.Background()
	svc := NewService(testPool, nil, "webhook-secret")

	orgID := uuid.New()
	if _, err := testPool.Exec(ctx, `
		INSERT INTO organizations (id, name, brand_name, slug)
		VALUES ($1, $2, $3, $4)`,
		orgID, "Sub Org", "Sub Org", "sub-"+uuid.NewString(),
	); err != nil {
		t.Fatalf("insert org: %v", err)
	}

	managerID := uuid.New()
	if _, err := testPool.Exec(ctx, `
		INSERT INTO users (id, organization_id, full_name, phone, email, role, is_super_manager, activated, status)
		VALUES ($1, $2, $3, $4, $5, 'manager', true, true, 'active')`,
		managerID, orgID, "Manager", uuid.NewString(), "sub-"+uuid.NewString()+"@example.com",
	); err != nil {
		t.Fatalf("insert user: %v", err)
	}

	reference := "ref-" + uuid.NewString()
	if _, err := testPool.Exec(ctx, `
		INSERT INTO subscription_charges (
			organization_id, manager_id, tier, amount_kes, gateway_provider,
			gateway_reference, status, billing_period_start, billing_period_end, raw_payload
		)
		VALUES ($1, $2, 'starter', 1500, 'intasend', $3, 'PENDING', CURRENT_DATE, CURRENT_DATE, '{}'::jsonb)`,
		orgID, managerID, reference,
	); err != nil {
		t.Fatalf("insert pending charge: %v", err)
	}

	body := []byte(`{"id":"` + reference + `","state":"COMPLETE"}`)
	sig := signBody(t, "webhook-secret", body)

	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodPost, "/v1/webhooks/intasend", bytes.NewReader(body))
		req.Header.Set("X-Intasend-Signature", sig)

		rr := httptest.NewRecorder()
		svc.IntasendWebhook(rr, req, nil)

		if rr.Code != http.StatusNoContent {
			t.Fatalf("call %d status = %d, want %d", i+1, rr.Code, http.StatusNoContent)
		}
	}

	var complete int
	if err := testPool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM subscription_charges
		WHERE gateway_provider = 'intasend'
		  AND gateway_reference = $1
		  AND status = 'COMPLETE'`,
		reference,
	).Scan(&complete); err != nil {
		t.Fatalf("count complete: %v", err)
	}
	if complete != 1 {
		t.Fatalf("complete rows = %d, want 1", complete)
	}
}

func signBody(t *testing.T, secret string, body []byte) string {
	t.Helper()
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}
